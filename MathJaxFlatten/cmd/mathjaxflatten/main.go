package main

import (
	"fmt"
	"image/png"
	"os"

	"mathjaxflatten"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Println("Usage: mathjaxflatten <input.svg> <output.png>")
		os.Exit(1)
	}

	in, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer in.Close()

	img, err := mathjaxflatten.Render(in, mathjaxflatten.DefaultOptions)
	if err != nil {
		panic(err)
	}

	out, err := os.Create(os.Args[2])
	if err != nil {
		panic(err)
	}
	defer out.Close()

	if err := png.Encode(out, img); err != nil {
		panic(err)
	}
	fmt.Println("Wrote", os.Args[2])
}
