# mathjaxflatten

A pure Go library to flatten MathJax SVGs into PNG or JPEG images. 

General purpose SVG parsers in pure Go often fail or panic on MathJax SVGs because of complex nested transforms and path data. This library implements a lightweight custom XML parser and path data parser specifically designed to handle the subset of SVG that MathJax produces.

It uses [rasterx](https://github.com/srwiley/rasterx) for high-quality 2D vector rasterization.

## Usage

### As a Library

```go
package main

import (
	"image/png"
	"os"

	"mathjaxflatten"
)

func main() {
	in, _ := os.Open("MathJaxExample.svg")
	defer in.Close()

	img, err := mathjaxflatten.Render(in, mathjaxflatten.DefaultOptions)
	if err != nil {
		panic(err)
	}

	out, _ := os.Create("out.png")
	defer out.Close()
	png.Encode(out, img)
}
```

### Options

You can configure the target width and background color:

```go
opts := mathjaxflatten.Options{
	TargetWidth: 1024, // Height will scale automatically
	Background:  color.White,
}
img, err := mathjaxflatten.Render(in, opts)
```

### As a CLI Tool

A simple CLI tool is provided to convert MathJax SVGs to PNGs.

```bash
go run ./cmd/mathjaxflatten MathJaxExample.svg out.png
```
