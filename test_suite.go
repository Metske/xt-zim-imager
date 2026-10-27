package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"os"
	"time"
)

type TestArticle struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

func parseBTreeNode(imgFile *os.File, indexStartSector uint32, nodeSector uint32) ([]byte, error) {
	block := make([]byte, 512)
	offset := int64(indexStartSector+nodeSector) * 512
	_, err := imgFile.Seek(offset, io.SeekStart)
	if err != nil {
		return nil, err
	}
	_, err = io.ReadFull(imgFile, block)
	return block, err
}

func lookupInBTree(imgFile *os.File, indexStartSector uint32, key []byte) (uint64, error) {
	nodeSector := uint32(0)
	
	if len(key) > 64 {
		key = key[:64]
	}

	for {
		block, err := parseBTreeNode(imgFile, indexStartSector, nodeSector)
		if err != nil {
			return 0, err
		}

		isLeaf := block[0] == 1
		numKeys := int(block[1])

		foundIdx := -1
		childIdx := numKeys

		for i := 0; i < numKeys; i++ {
			k := block[4+i*64 : 4+i*64+64]
			k = bytes.TrimRight(k, "\x00")

			cmp := bytes.Compare(key, k)
			if cmp == 0 {
				foundIdx = i
				break
			} else if cmp < 0 {
				childIdx = i
				break
			}
		}

		if foundIdx != -1 {
			return binary.LittleEndian.Uint64(block[388+foundIdx*8 : 388+foundIdx*8+8]), nil
		}

		if isLeaf {
			return 0, fmt.Errorf("key not found")
		}

		nodeSector = binary.LittleEndian.Uint32(block[436+childIdx*4 : 436+childIdx*4+4])
	}
}

func readArticleAtOffset(imgFile *os.File, targetOffset uint64) (string, error) {
	physicalOffset := (targetOffset >> 32) * 32
	uncompressedOffset := targetOffset & 0xFFFFFFFF
	
	if uncompressedOffset == 0xFFFFFFFF {
		return "[IMAGE DATA PNG/JPEG]", nil
	}
	
	_, err := imgFile.Seek(int64(physicalOffset), io.SeekStart)
	if err != nil {
		return "", err
	}
	
	magic := make([]byte, 4)
	io.ReadFull(imgFile, magic)
	if magic[0] != 0x1F || magic[1] != 0x8B || magic[2] != 0x08 {
		return "", fmt.Errorf("not a gzip stream (magic %x %x %x)", magic[0], magic[1], magic[2])
	}
	
	imgFile.Seek(int64(physicalOffset), io.SeekStart)
	gr, err := gzip.NewReader(imgFile)
	if err != nil {
		return "", err
	}
	gr.Multistream(false)
	defer gr.Close()
	
	buf, err := io.ReadAll(gr)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", err
	}
	
	if int(uncompressedOffset)+4 > len(buf) {
		return "", fmt.Errorf("uncompressed offset out of bounds")
	}
	
	length := binary.LittleEndian.Uint32(buf[uncompressedOffset : uncompressedOffset+4])
	
	if int(uncompressedOffset+4+uint64(length)) > len(buf) {
		return "", fmt.Errorf("article length out of bounds")
	}
	
	articleData := buf[uncompressedOffset+4 : uncompressedOffset+4+uint64(length)]
	
	return string(articleData), nil
}

func (a *App) TestLookupArticle(imgPath string, title string) (*TestArticle, error) {
	imgFile, err := os.Open(imgPath)
	if err != nil {
		return nil, err
	}
	defer imgFile.Close()

	imgFile.Seek(-512, io.SeekEnd)
	footer := make([]byte, 512)
	io.ReadFull(imgFile, footer)
	if string(footer[0:4]) != "CPR1" {
		return nil, fmt.Errorf("not a valid img file (missing CPR1)")
	}

	indexStartSector := binary.LittleEndian.Uint32(footer[4:8])

	key := []byte(title)
	sector, err := lookupInBTree(imgFile, indexStartSector, key)
	if err != nil {
		// try lower case
		keyLower := bytes.ToLower(key)
		sector, err = lookupInBTree(imgFile, indexStartSector, keyLower)
		if err != nil {
			return nil, fmt.Errorf("article not found: %v", err)
		}
	}

	content, err := readArticleAtOffset(imgFile, sector)
	if err != nil {
		return nil, err
	}

	return &TestArticle{
		Title:   title,
		Content: content,
	}, nil
}

func (a *App) TestRandomArticle(imgPath string) (*TestArticle, error) {
	imgFile, err := os.Open(imgPath)
	if err != nil {
		return nil, err
	}
	defer imgFile.Close()

	imgFile.Seek(-512, io.SeekEnd)
	footer := make([]byte, 512)
	io.ReadFull(imgFile, footer)
	if string(footer[0:4]) != "CPR1" {
		return nil, fmt.Errorf("not a valid img file (missing CPR1)")
	}

	indexStartSector := binary.LittleEndian.Uint32(footer[4:8])

	// To get a random article, we pick a random leaf node in the BTree
	// Let's just traverse down randomly
	nodeSector := uint32(0)
	rand.Seed(time.Now().UnixNano())

	var title string
	var targetOffset uint64

	for {
		block, err := parseBTreeNode(imgFile, indexStartSector, nodeSector)
		if err != nil {
			return nil, err
		}

		isLeaf := block[0] == 1
		numKeys := int(block[1])

		if isLeaf {
			if numKeys == 0 {
				return nil, fmt.Errorf("empty leaf node")
			}
			idx := rand.Intn(numKeys)
			k := block[4+idx*64 : 4+idx*64+64]
			title = string(bytes.TrimRight(k, "\x00"))
			targetOffset = binary.LittleEndian.Uint64(block[388+idx*8 : 388+idx*8+8])
			break
		} else {
			// random child
			idx := rand.Intn(numKeys + 1)
			nodeSector = binary.LittleEndian.Uint32(block[436+idx*4 : 436+idx*4+4])
		}
	}

	content, err := readArticleAtOffset(imgFile, targetOffset)
	if err != nil {
		return nil, err
	}

	return &TestArticle{
		Title:   title,
		Content: content,
	}, nil
}

func (a *App) TestLookupImage(imgPath string, hash string) (string, error) {
	imgFile, err := os.Open(imgPath)
	if err != nil {
		return "", err
	}
	defer imgFile.Close()

	imgFile.Seek(-512, io.SeekEnd)
	footer := make([]byte, 512)
	io.ReadFull(imgFile, footer)
	if string(footer[0:4]) != "CPR1" {
		return "", fmt.Errorf("not a valid img file (missing CPR1)")
	}

	indexStartSector := binary.LittleEndian.Uint32(footer[4:8])

	key := []byte(hash)
	sector, err := lookupInBTree(imgFile, indexStartSector, key)
	if err != nil {
		return "", fmt.Errorf("image not found: %v", err)
	}

	physicalOffset := (sector >> 32) * 32
	imgFile.Seek(int64(physicalOffset), io.SeekStart)
	buf := make([]byte, 256*1024)
	n, _ := io.ReadAtLeast(imgFile, buf, 8)
	buf = buf[:n]

	idxPNG := bytes.Index(buf, []byte("IEND"))
	if idxPNG != -1 && idxPNG+8 <= len(buf) {
		buf = buf[:idxPNG+8]
	} else {
		idxJPEG := bytes.Index(buf, []byte{0xFF, 0xD9})
		if idxJPEG != -1 && idxJPEG+2 <= len(buf) {
			buf = buf[:idxJPEG+2]
		}
	}

	return base64.StdEncoding.EncodeToString(buf), nil
}
