package main

import (
	"context"
	"fmt"
	"os"
)

func runCLI() {
	if len(os.Args) < 3 {
		fmt.Println("Usage:")
		fmt.Println("  xt-zim-imager process-image <input.zim> <output.img>")
		fmt.Println("  xt-zim-imager append-font <input.img>")
		os.Exit(1)
	}

	cmd := os.Args[1]
	if cmd == "process-image" {
		if len(os.Args) < 4 {
			fmt.Println("Usage: xt-zim-imager process-image <input.zim> <output.img>")
			os.Exit(1)
		}
		zimPath := os.Args[2]
		outPath := os.Args[3]
		fmt.Printf("Processing %s to %s...\n", zimPath, outPath)

		outFile, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0644)
		if err != nil {
			fmt.Printf("Failed to create image file: %v\n", err)
			os.Exit(1)
		}
		defer outFile.Close()

		ctx := context.Background()
		_, _, _, err = ProcessZIM(ctx, zimPath, outFile, 0, true, 1, "floydsteinberg", "jpeg")
		if err != nil {
			fmt.Printf("Processing failed: %v\n", err)
			os.Exit(1)
		}
		outFile.Sync()
		fmt.Println("Processing complete!")
	} else if cmd == "append-font" {
		imgPath := os.Args[2]
		fmt.Printf("Appending fallback_noto.bin to %s...\n", imgPath)

		ctx := context.Background()
		err := AppendFontToImage(ctx, imgPath)
		if err != nil {
			fmt.Printf("Append failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Append complete!")
	} else {
		fmt.Println("Unknown command:", cmd)
	}
}
