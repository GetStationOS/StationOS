package artwork

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestNormalizationBoundsRasterAndFlattensTransparency(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 1024, 512))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 0})
	var data bytes.Buffer
	_ = png.Encode(&data, src)
	normalized, err := Normalize(data.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	result, format, err := image.Decode(bytes.NewReader(normalized))
	if err != nil || format != "jpeg" || result.Bounds().Dx() != 768 || result.Bounds().Dy() != 384 {
		t.Fatal(format, err, result.Bounds())
	}
	r, g, b, _ := result.At(0, 0).RGBA()
	if r < 65000 || g < 65000 || b < 65000 {
		t.Fatal("transparent pixel was not flattened to white", r, g, b)
	}
}
func TestArtworkRejectsInvalidAndOversizedInput(t *testing.T) {
	for _, data := range [][]byte{nil, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`), make([]byte, (5<<20)+1)} {
		if _, err := Normalize(data); err == nil {
			t.Fatal("invalid image accepted")
		}
	}
}
