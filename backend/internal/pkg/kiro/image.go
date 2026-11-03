package kiro

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

func ConvertImage(source *apicompat.AnthropicImageSource) (Image, error) {
	if source == nil || source.Type != "base64" {
		return Image{}, errors.New("Kiro images require a base64 image source")
	}
	if len(source.Data) > 12<<20 {
		return Image{}, errors.New("Kiro image exceeds the 8 MiB decoded limit")
	}
	data, err := base64.StdEncoding.DecodeString(source.Data)
	if err != nil || len(data) > 8<<20 {
		return Image{}, errors.New("invalid or oversized base64 image")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 ||
		int64(config.Width)*int64(config.Height) > 40_000_000 {
		return Image{}, errors.New("invalid or oversized image dimensions")
	}
	if config.Width <= 1536 && config.Height <= 1536 && len(data) <= 384<<10 {
		return Image{Format: format, Source: map[string]string{"bytes": source.Data}}, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, errors.New("could not decode image")
	}
	width, height := config.Width, config.Height
	for max(width, height) > 1536 {
		width, height = max(1, width/2), max(1, height/2)
	}
	for {
		resized := image.NewRGBA(image.Rect(0, 0, width, height))
		draw.ApproxBiLinear.Scale(resized, resized.Bounds(), img, img.Bounds(), draw.Over, nil)
		var encoded bytes.Buffer
		if err := jpeg.Encode(&encoded, resized, &jpeg.Options{Quality: 80}); err != nil {
			return Image{}, errors.New("could not encode image")
		}
		if encoded.Len() <= 384<<10 {
			return Image{Format: "jpeg", Source: map[string]string{"bytes": base64.StdEncoding.EncodeToString(encoded.Bytes())}}, nil
		}
		if min(width, height) <= 64 {
			return Image{}, errors.New("image cannot fit Kiro's field size limit")
		}
		width, height = max(1, width/2), max(1, height/2)
	}
}

func IsImageMediaType(mediaType string) bool {
	switch strings.ToLower(mediaType) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}
