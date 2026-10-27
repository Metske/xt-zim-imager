package zim

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"sync"

	lru "github.com/hashicorp/golang-lru"
	"github.com/klauspost/compress/zstd"
)

const (
	RedirectEntry   uint16 = 0xffff
	LinkTargetEntry        = 0xfffe
	DeletedEntry           = 0xfffd
)

var articlePool sync.Pool

// the recent uncompressed blobs, mainly useful while indexing and asking
// for the same blob again and again
var bcache *lru.ARCCache

type Article struct {
	// EntryType is a RedirectEntry/LinkTargetEntry/DeletedEntry or an idx
	// pointing to ZimReader.mimeTypeList
	EntryType uint16
	Title     string
	URLPtr    uint64
	Namespace byte
	url       string
	blob      uint32
	cluster   uint32
	z         *ZimReader
}

// Cluster returns the cluster id
func (a *Article) Cluster() uint32 {
	return a.cluster
}

// Blob returns the blob index within the cluster
func (a *Article) Blob() uint32 {
	return a.blob
}

// URL returns the raw article URL without the namespace prefix
func (a *Article) URL() string {
	return a.url
}

// convenient method to return the Article at URL index idx
func (z *ZimReader) ArticleAtURLIdx(idx uint32) (*Article, error) {
	o, err := z.OffsetAtURLIdx(idx)
	if err != nil {
		return nil, err
	}
	return z.ArticleAt(o)
}

// return the article main page if it exists
func (z *ZimReader) MainPage() (*Article, error) {
	if z.mainPage == 0xffffffff {
		return nil, nil
	}
	return z.ArticleAtURLIdx(z.mainPage)
}

// get the article (Directory) pointed by the offset found in URLpos or Titlepos
func (z *ZimReader) ArticleAt(offset uint64) (*Article, error) {
	a := articlePool.Get().(*Article)
	err := z.FillArticleAt(a, offset)
	return a, err
}

func parseArticleFromBytes(z *ZimReader, a *Article, offset uint64, b []byte, parseStrings bool) (hasTitleOrURL bool, err error) {
	if len(b) < 2 {
		return false, io.ErrUnexpectedEOF
	}
	a.z = z
	a.URLPtr = offset
	mimeIdx := binary.LittleEndian.Uint16(b[0:2])
	a.EntryType = mimeIdx

	if mimeIdx == LinkTargetEntry || mimeIdx == DeletedEntry {
		return false, nil
	}

	if mimeIdx == RedirectEntry {
		if len(b) < 12 {
			return false, io.ErrUnexpectedEOF
		}
		a.Namespace = b[3]
		a.cluster = binary.LittleEndian.Uint32(b[8:12])
		a.blob = 0
		rest := b[12:]
		nul1 := bytes.IndexByte(rest, 0)
		if nul1 < 0 {
			return false, io.ErrUnexpectedEOF
		}
		a.url = string(rest[:nul1])
		rest2 := rest[nul1+1:]
		nul2 := bytes.IndexByte(rest2, 0)
		if nul2 < 0 {
			return false, io.ErrUnexpectedEOF
		}
		a.Title = string(rest2[:nul2])
		return len(a.Title) > 0 || len(a.url) > 0, nil
	}

	if len(b) < 18 {
		return false, io.ErrUnexpectedEOF
	}
	a.Namespace = b[3]
	a.cluster = binary.LittleEndian.Uint32(b[8:12])
	a.blob = binary.LittleEndian.Uint32(b[12:16])

	if !parseStrings {
		// Fast path for Phase 1 directory scan: check that at least one of url or title is non-empty
		return b[16] != 0 || b[17] != 0, nil
	}

	rest := b[16:]
	nul1 := bytes.IndexByte(rest, 0)
	if nul1 < 0 {
		return false, io.ErrUnexpectedEOF
	}
	a.url = string(rest[:nul1])
	rest2 := rest[nul1+1:]
	nul2 := bytes.IndexByte(rest2, 0)
	if nul2 < 0 {
		return false, io.ErrUnexpectedEOF
	}
	a.Title = string(rest2[:nul2])
	return len(a.Title) > 0 || len(a.url) > 0, nil
}

// Fill an article with datas found at offset
func (z *ZimReader) FillArticleAt(a *Article, offset uint64) error {
	var buf [2064]byte
	n, err := z.f.ReadAt(buf[:], int64(offset))
	if n == 0 && err != nil {
		return fmt.Errorf("can't read article %w", err)
	}
	_, err = parseArticleFromBytes(z, a, offset, buf[:n], true)
	return err
}

// DirReader provides worker-local buffered reading of URL pointers and directory entries.
type DirReader struct {
	z           *ZimReader
	urlPtrBuf   []byte
	urlPtrBase  uint32
	urlPtrCount uint32
	dirBuf      []byte
	dirBase     uint64
	dirLen      int
	dirEOF      bool
}

func (z *ZimReader) NewDirReader() *DirReader {
	return &DirReader{
		z:         z,
		urlPtrBuf: make([]byte, 64*1024),  // 8,192 URL pointers per read
		dirBuf:    make([]byte, 128*1024), // 128 KB sliding window of directory entries
	}
}

func (d *DirReader) OffsetAtURLIdx(idx uint32) (uint64, error) {
	if idx < d.urlPtrBase || idx >= d.urlPtrBase+d.urlPtrCount {
		fileOff := d.z.urlPtrPos + uint64(idx)*8
		n, err := d.z.f.ReadAt(d.urlPtrBuf, int64(fileOff))
		if n < 8 {
			if err != nil {
				return 0, err
			}
			return 0, io.ErrUnexpectedEOF
		}
		d.urlPtrBase = idx
		d.urlPtrCount = uint32(n / 8)
	}
	rel := (idx - d.urlPtrBase) * 8
	return binary.LittleEndian.Uint64(d.urlPtrBuf[rel : rel+8]), nil
}

func (d *DirReader) sliceAt(offset uint64) ([]byte, error) {
	const minEntryWindow = 2064
	if d.dirLen == 0 || offset < d.dirBase || offset >= d.dirBase+uint64(d.dirLen) ||
		(int(offset-d.dirBase)+minEntryWindow > d.dirLen && !d.dirEOF) {
		n, err := d.z.f.ReadAt(d.dirBuf, int64(offset))
		if n == 0 {
			if err != nil {
				return nil, err
			}
			return nil, io.ErrUnexpectedEOF
		}
		d.dirBase = offset
		d.dirLen = n
		d.dirEOF = n < len(d.dirBuf)
	}
	rel := int(offset - d.dirBase)
	return d.dirBuf[rel:d.dirLen], nil
}

// ReadPhase1Entry reads directory entry metadata at idx. For non-redirect entries,
// it avoids allocating URL and Title strings and returns whether the entry has a non-empty URL or Title.
func (d *DirReader) ReadPhase1Entry(idx uint32, a *Article) (hasTitleOrURL bool, err error) {
	offset, err := d.OffsetAtURLIdx(idx)
	if err != nil {
		return false, err
	}
	b, err := d.sliceAt(offset)
	if err != nil {
		return false, err
	}
	hasTitleOrURL, err = parseArticleFromBytes(d.z, a, offset, b, false)
	if err == io.ErrUnexpectedEOF && d.dirLen > 0 {
		// Entry spanned past the end of dirBuf; force a fresh read starting at offset
		d.dirLen = 0
		b, err = d.sliceAt(offset)
		if err != nil {
			return false, err
		}
		hasTitleOrURL, err = parseArticleFromBytes(d.z, a, offset, b, false)
	}
	return hasTitleOrURL, err
}

// FillArticleAtURLIdx populates all fields of a (including URL and Title) for URL index idx.
func (d *DirReader) FillArticleAtURLIdx(idx uint32, a *Article) error {
	offset, err := d.OffsetAtURLIdx(idx)
	if err != nil {
		return err
	}
	b, err := d.sliceAt(offset)
	if err != nil {
		return err
	}
	_, err = parseArticleFromBytes(d.z, a, offset, b, true)
	if err == io.ErrUnexpectedEOF && d.dirLen > 0 {
		d.dirLen = 0
		b, err = d.sliceAt(offset)
		if err != nil {
			return err
		}
		_, err = parseArticleFromBytes(d.z, a, offset, b, true)
	}
	return err
}

// ClusterReader provides worker-local cluster reading and decompression without global cache contention.
type ClusterReader struct {
	z           *ZimReader
	clusterIdx  uint32
	hasCluster  bool
	compression uint8
	compBuf     []byte
	decompBuf   []byte
	zstdDec     *zstd.Decoder
}

func (z *ZimReader) NewClusterReader() (*ClusterReader, error) {
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	return &ClusterReader{
		z:       z,
		zstdDec: dec,
	}, nil
}

func (cr *ClusterReader) Close() {
	if cr.zstdDec != nil {
		cr.zstdDec.Close()
	}
}

func (cr *ClusterReader) LoadCluster(clusterIdx uint32) error {
	if cr.hasCluster && cr.clusterIdx == clusterIdx {
		return nil
	}
	start, end, err := cr.z.clusterOffsetsAtIdx(clusterIdx)
	if err != nil {
		return err
	}
	if end < start {
		return errors.New("invalid cluster bounds")
	}
	clusterLen := int(end - start + 1)
	if cap(cr.compBuf) < clusterLen {
		cr.compBuf = make([]byte, clusterLen)
	} else {
		cr.compBuf = cr.compBuf[:clusterLen]
	}
	n, err := cr.z.f.ReadAt(cr.compBuf, int64(start))
	if n != clusterLen {
		if err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	}

	cr.compression = cr.compBuf[0]
	compType := cr.compression & 0x0f
	switch compType {
	case 5:
		cr.decompBuf, err = cr.zstdDec.DecodeAll(cr.compBuf[1:], cr.decompBuf[:0])
		if err != nil {
			return err
		}
	case 4:
		dec, err := NewXZReader(bytes.NewReader(cr.compBuf[1:]))
		if err != nil {
			return err
		}
		cr.decompBuf, err = ioutil.ReadAll(dec)
		dec.Close()
		if err != nil {
			return err
		}
	case 0, 1:
		// Uncompressed data lives directly in cr.compBuf[1:]
	default:
		return errors.New("Unhandled compression")
	}

	cr.clusterIdx = clusterIdx
	cr.hasCluster = true
	return nil
}

func (cr *ClusterReader) BlobData(blobIdx uint32) ([]byte, error) {
	if !cr.hasCluster {
		return nil, errors.New("no cluster loaded")
	}
	var buf []byte
	compType := cr.compression & 0x0f
	if compType == 0 || compType == 1 {
		buf = cr.compBuf[1:]
	} else {
		buf = cr.decompBuf
	}

	if cr.compression&0x10 != 0 {
		off := uint64(blobIdx) * 8
		if off+16 > uint64(len(buf)) {
			return nil, io.ErrUnexpectedEOF
		}
		bs := binary.LittleEndian.Uint64(buf[off : off+8])
		be := binary.LittleEndian.Uint64(buf[off+8 : off+16])
		if bs > be || be > uint64(len(buf)) {
			return nil, io.ErrUnexpectedEOF
		}
		return buf[bs:be], nil
	}

	off := uint64(blobIdx) * 4
	if off+8 > uint64(len(buf)) {
		return nil, io.ErrUnexpectedEOF
	}
	bs := binary.LittleEndian.Uint32(buf[off : off+4])
	be := binary.LittleEndian.Uint32(buf[off+4 : off+8])
	if bs > be || uint64(be) > uint64(len(buf)) {
		return nil, io.ErrUnexpectedEOF
	}
	return buf[bs:be], nil
}

// return the uncompressed data associated with this article
func (a *Article) Data() ([]byte, error) {
	// ensure we have data to read
	if a.EntryType == RedirectEntry || a.EntryType == LinkTargetEntry || a.EntryType == DeletedEntry {
		return nil, nil
	}
	start, end, err := a.z.clusterOffsetsAtIdx(a.cluster)
	if err != nil {
		return nil, err
	}
	s, err := a.z.bytesRangeAt(start, start+1)
	if err != nil {
		return nil, err
	}
	compression := uint8(s[0])
	compType := compression & 0x0f
	isExtended := (compression & 0x10) != 0

	// LZMA: 4, Zstandard: 5
	if compType == 4 || compType == 5 {
		blobLookup := func() ([]byte, bool) {
			if v, ok := bcache.Get(a.cluster); ok {
				b := v.([]byte)
				return b, ok
			}
			return nil, false
		}

		var blob []byte
		var ok bool
		var dec io.ReadCloser
		if blob, ok = blobLookup(); !ok {
			b, err := a.z.bytesRangeAt(start+1, end+1)
			if err != nil {
				return nil, err
			}
			bbuf := bytes.NewBuffer(b)
			switch compType {
			case 5:
				dec, err = NewZstdReader(bbuf)

			case 4:
				dec, err = NewXZReader(bbuf)
			}
			if err != nil {
				return nil, err
			}
			defer dec.Close()
			// the decoded chunk are around 1MB
			b, err = ioutil.ReadAll(dec)
			if err != nil {
				return nil, err
			}
			blob = b
			bcache.Add(a.cluster, blob)
		}

		var bs, be uint64
		if isExtended {
			off := uint64(a.blob) * 8
			if off+16 > uint64(len(blob)) {
				return nil, io.ErrUnexpectedEOF
			}
			bs = binary.LittleEndian.Uint64(blob[off : off+8])
			be = binary.LittleEndian.Uint64(blob[off+8 : off+16])
		} else {
			off := uint64(a.blob) * 4
			if off+8 > uint64(len(blob)) {
				return nil, io.ErrUnexpectedEOF
			}
			bs = uint64(binary.LittleEndian.Uint32(blob[off : off+4]))
			be = uint64(binary.LittleEndian.Uint32(blob[off+4 : off+8]))
		}
		if bs > be || be > uint64(len(blob)) {
			return nil, io.ErrUnexpectedEOF
		}

		// avoid retaining all the chunk
		c := make([]byte, be-bs)
		copy(c, blob[bs:be])
		return c, nil

	} else if compType == 0 || compType == 1 {
		// uncompressed
		startPos := start + 1
		if isExtended {
			blobOffset := uint64(a.blob) * 8
			bs, err := readInt64(a.z.bytesRangeAt(startPos+blobOffset, startPos+blobOffset+8))
			if err != nil {
				return nil, err
			}
			be, err := readInt64(a.z.bytesRangeAt(startPos+blobOffset+8, startPos+blobOffset+16))
			if err != nil {
				return nil, err
			}
			return a.z.bytesRangeAt(startPos+bs, startPos+be)
		}
		blobOffset := uint64(a.blob * 4)

		bs, err := readInt32(a.z.bytesRangeAt(startPos+blobOffset, startPos+blobOffset+4))
		if err != nil {
			return nil, err
		}

		be, err := readInt32(a.z.bytesRangeAt(startPos+blobOffset+4, startPos+blobOffset+4+4))
		if err != nil {
			return nil, err
		}

		return a.z.bytesRangeAt(startPos+uint64(bs), startPos+uint64(be))
	}

	return nil, errors.New("Unhandled compression")
}

func (a *Article) MimeType() string {
	if a.EntryType == RedirectEntry || a.EntryType == LinkTargetEntry || a.EntryType == DeletedEntry {
		return ""
	}
	if int(a.EntryType) >= len(a.z.mimeTypeList) {
		return ""
	}

	return a.z.mimeTypeList[a.EntryType]
}

// return the url prefixed by the namespace
func (a *Article) FullURL() string {
	return string(a.Namespace) + "/" + a.url
}

func (a *Article) String() string {
	return fmt.Sprintf("Mime: 0x%x URL: [%s], Title: [%s], Cluster: 0x%x Blob: 0x%x",
		a.EntryType, a.FullURL(), a.Title, a.cluster, a.blob)
}

// RedirectIndex return the redirect index of RedirectEntry type article
// return an err if not a redirect entry
func (a *Article) RedirectIndex() (uint32, error) {
	if a.EntryType != RedirectEntry {
		return 0, errors.New("Not a RedirectEntry")
	}
	// We use the cluster to save the redirect index position for RedirectEntry type
	return a.cluster, nil
}

func (a *Article) blobOffsetsAtIdx(z *ZimReader) (start, end uint64) {
	idx := a.blob
	offset := z.clusterPtrPos + uint64(idx)*8
	start, err := readInt64(z.bytesRangeAt(offset, offset+8))
	if err != nil {
		return
	}
	offset = z.clusterPtrPos + uint64(idx+1)*8
	end, _ = readInt64(z.bytesRangeAt(offset, offset+8))

	return
}
