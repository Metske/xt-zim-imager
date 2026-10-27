package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx context.Context
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) SelectZIM() string {
	filepath, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select ZIM File",
		Filters: []runtime.FileFilter{
			{DisplayName: "ZIM Files", Pattern: "*.zim"},
		},
	})
	if err != nil {
		return ""
	}
	return filepath
}

func (a *App) SelectImgIn() string {
	filepath, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select .img File",
		Filters: []runtime.FileFilter{
			{DisplayName: "Image Files", Pattern: "*.img"},
		},
	})
	if err != nil {
		return ""
	}
	return filepath
}

func (a *App) SelectTargetDir() string {
	dirpath, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Target Folder on SD Card",
	})
	if err != nil {
		return ""
	}
	return dirpath
}

func (a *App) SelectImgOut() string {
	filepath, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title: "Save Image File",
		Filters: []runtime.FileFilter{
			{DisplayName: "Image Files", Pattern: "*.img"},
		},
	})
	if err != nil {
		return ""
	}
	return filepath
}

func (a *App) StartProcessingImg(zimPath string, outPath string, processImages bool, imageBitDepth int, ditherAlgo string, imageFormat string) error {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				os.WriteFile("crash.log", []byte(fmt.Sprintf("PANIC in Goroutine: %v", r)), 0666)
				runtime.EventsEmit(a.ctx, "error", fmt.Sprintf("App crashed! See crash.log. Error: %v", r))
			}
		}()

		runtime.EventsEmit(a.ctx, "log", fmt.Sprintf("Creating image file %s ...", outPath))
		outFile, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0644)
		if err != nil {
			runtime.EventsEmit(a.ctx, "error", fmt.Sprintf("Failed to create image file: %v", err))
			return
		}
		defer outFile.Close()

		_, _, _, err = ProcessZIM(a.ctx, zimPath, outFile, 0, processImages, imageBitDepth, ditherAlgo, imageFormat)
		if err != nil {
			runtime.EventsEmit(a.ctx, "error", fmt.Sprintf("Processing failed: %v", err))
			return
		}

		outFile.Sync()
		runtime.EventsEmit(a.ctx, "done", true)
	}()
	return nil
}

func (a *App) StartAppendingFont(imgPath string) error {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				os.WriteFile("crash.log", []byte(fmt.Sprintf("PANIC in Goroutine: %v", r)), 0666)
				runtime.EventsEmit(a.ctx, "error", fmt.Sprintf("App crashed! See crash.log. Error: %v", r))
			}
		}()

		runtime.EventsEmit(a.ctx, "log", fmt.Sprintf("Appending unifont.bin to %s ...", imgPath))
		err := AppendFontToImage(a.ctx, imgPath)
		if err != nil {
			runtime.EventsEmit(a.ctx, "error", fmt.Sprintf("Append failed: %v", err))
			return
		}
	}()
	return nil
}

func (a *App) CopyImgContiguous(imgPath string, targetDir string) error {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				runtime.EventsEmit(a.ctx, "error", fmt.Sprintf("Copy crashed! %v", r))
			}
		}()

		info, err := os.Stat(imgPath)
		if err != nil {
			runtime.EventsEmit(a.ctx, "error", fmt.Sprintf("Failed to read source img: %v", err))
			return
		}
		size := info.Size()

		targetFile := filepath.Join(targetDir, filepath.Base(imgPath))
		runtime.EventsEmit(a.ctx, "log", fmt.Sprintf("Creating target file %s (Preallocating %d bytes)...", targetFile, size))

		out, err := os.Create(targetFile)
		if err != nil {
			runtime.EventsEmit(a.ctx, "error", fmt.Sprintf("Failed to create target file: %v", err))
			return
		}

		// Preallocate
		err = out.Truncate(size)
		if err != nil {
			out.Close()
			os.Remove(targetFile)
			runtime.EventsEmit(a.ctx, "error", fmt.Sprintf("Failed to preallocate space. Ensure the SD card has enough free space: %v", err))
			return
		}

		// Reset pointer to 0
		out.Seek(0, 0)

		in, err := os.Open(imgPath)
		if err != nil {
			out.Close()
			runtime.EventsEmit(a.ctx, "error", fmt.Sprintf("Failed to open source file: %v", err))
			return
		}

		runtime.EventsEmit(a.ctx, "log", "Space preallocated successfully. Copying data...")

		// Copy with progress
		buf := make([]byte, 4*1024*1024)
		var written int64

		// simple manual copy loop to emit progress
		for {
			n, readErr := in.Read(buf)
			if n > 0 {
				_, writeErr := out.Write(buf[:n])
				if writeErr != nil {
					runtime.EventsEmit(a.ctx, "error", fmt.Sprintf("Write error during copy: %v", writeErr))
					break
				}
				written += int64(n)
				runtime.EventsEmit(a.ctx, "progress", float64(written)/float64(size))
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				runtime.EventsEmit(a.ctx, "error", fmt.Sprintf("Read error during copy: %v", readErr))
				break
			}
		}

		in.Close()
		out.Sync()
		out.Close()

		if written == size {
			runtime.EventsEmit(a.ctx, "log", "Copy completed successfully and contiguously!")
			runtime.EventsEmit(a.ctx, "done", true)
		}
	}()
	return nil
}
