package web

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func photoForm(t *testing.T, h http.Handler, fields url.Values, data []byte, hx bool) *httptest.ResponseRecorder {
	return imageForm(t, h, "/producer/djs", fields, data, hx)
}
func imageForm(t *testing.T, h http.Handler, path string, fields url.Values, data []byte, hx bool) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, values := range fields {
		for _, value := range values {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if data != nil {
		part, err := writer.CreateFormFile("photo", "photo.png")
		if err != nil {
			t.Fatal(err)
		}
		part.Write(data)
	}
	writer.Close()
	req := httptest.NewRequest("POST", path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if hx {
		req.Header.Set("HX-Request", "true")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
func photoPNG(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1000, 600))
	for y := 0; y < 600; y++ {
		for x := 0; x < 1000; x++ {
			img.Set(x, y, c)
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestDJPhotoUploadReplaceRetainRemoveAndDelete(t *testing.T) {
	h, st, _, _ := setupWeb(t, "")
	token := csrf(t, h)
	fields := url.Values{"csrf": {token}, "name": {"Upload DJ"}, "model": {"gpt-6.1-sol"}, "voice": {"coral"}, "api_key": {"sk-test-photo"}}
	if w := photoForm(t, h, fields, photoPNG(t, color.RGBA{R: 200, A: 255}), true); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	var id, photoURL string
	if err := st.DB.QueryRow(`SELECT id,portrait_url FROM djs WHERE name='Upload DJ'`).Scan(&id, &photoURL); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(photoURL, "/djs/"+id+"/photo?v=") {
		t.Fatal("photo not managed by app", photoURL)
	}
	original := request(h, "GET", photoURL, nil, nil)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(original.Body.Bytes()))
	if original.Code != 200 || format != "jpeg" || cfg.Width != 768 || cfg.Height != 460 || err != nil {
		t.Fatal("photo not normalized", original.Code, cfg, format, err)
	}
	fields.Set("id", id)
	fields.Set("api_key", "")
	// Ordinary edits cannot erase a photo, and a submitted URL is ignored.
	fields.Set("portrait_url", "https://example.com/untrusted.jpg")
	if w := photoForm(t, h, fields, nil, false); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	var retained string
	st.DB.QueryRow(`SELECT portrait_url FROM djs WHERE id=?`, id).Scan(&retained)
	if retained != photoURL {
		t.Fatal("ordinary edit replaced photo")
	}
	// Failed profile validation commits neither the replacement nor settings.
	fields.Set("name", "")
	if w := photoForm(t, h, fields, photoPNG(t, color.RGBA{B: 200, A: 255}), true); w.Code != 422 {
		t.Fatal(w.Code)
	}
	if got := request(h, "GET", photoURL, nil, nil); !bytes.Equal(got.Body.Bytes(), original.Body.Bytes()) {
		t.Fatal("invalid form changed photo")
	}
	fields.Set("name", "Upload DJ")
	if w := photoForm(t, h, fields, photoPNG(t, color.RGBA{B: 200, A: 255}), false); w.Code != 303 {
		t.Fatal(w.Code, w.Body.String())
	}
	st.DB.QueryRow(`SELECT portrait_url FROM djs WHERE id=?`, id).Scan(&retained)
	if retained == photoURL {
		t.Fatal("replacement did not version photo")
	}
	fields.Set("photo_removed", "yes")
	if w := photoForm(t, h, fields, nil, true); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(h, "GET", "/djs/"+id+"/photo", nil, nil); w.Code != 404 {
		t.Fatal("removed photo still served")
	}
	fields.Del("photo_removed")
	if w := photoForm(t, h, fields, photoPNG(t, color.RGBA{G: 200, A: 255}), false); w.Code != 303 {
		t.Fatal(w.Code)
	}
	var hasKey bool
	st.DB.QueryRow(`SELECT length(api_key_cipher)>0 FROM djs WHERE id=?`, id).Scan(&hasKey)
	if !hasKey {
		t.Fatal("photo edit erased credentials")
	}
	if w := request(h, "POST", "/producer/djs/"+id+"/delete", url.Values{"csrf": {token}, "confirm": {"yes"}}, nil); w.Code != 303 {
		t.Fatal(w.Code)
	}
	var count int
	st.DB.QueryRow(`SELECT count(*) FROM dj_photos WHERE dj_id=?`, id).Scan(&count)
	if count != 0 {
		t.Fatal("deleted DJ left photo behind")
	}
}
func TestDJPhotoRejectsInvalidOversizedAndUnprotectedWrites(t *testing.T) {
	h, st, _, _ := setupWeb(t, "")
	fields := url.Values{"csrf": {csrf(t, h)}, "id": {"default-pop"}, "name": {"Alex"}, "model": {"gpt-6.1-sol"}, "voice": {"coral"}}
	largePixels := photoPNG(t, color.White)
	binary.BigEndian.PutUint32(largePixels[16:20], 4000)
	binary.BigEndian.PutUint32(largePixels[20:24], 4000)
	binary.BigEndian.PutUint32(largePixels[29:33], crc32.ChecksumIEEE(largePixels[12:29]))
	for _, data := range [][]byte{[]byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>"), bytes.Repeat([]byte{'x'}, (5<<20)+1), largePixels} {
		w := photoForm(t, h, fields, data, true)
		if w.Code != 422 {
			t.Fatal("invalid photo accepted", w.Code, w.Body.String())
		}
	}
	fields.Set("clear_photo", "yes")
	if w := photoForm(t, h, fields, photoPNG(t, color.White), true); w.Code != 422 {
		t.Fatal("conflicting removal accepted")
	}
	fields.Del("clear_photo")
	fields.Set("csrf", "wrong")
	if w := photoForm(t, h, fields, photoPNG(t, color.White), true); w.Code != 403 {
		t.Fatal("upload bypassed CSRF")
	}
	var count int
	st.DB.QueryRow(`SELECT count(*) FROM dj_photos`).Scan(&count)
	if count != 0 {
		t.Fatal("failed upload wrote data")
	}
	protected, _, _, _ := setupWeb(t, "secret")
	if w := request(protected, "GET", "/producer/djs/default-pop/photo", nil, nil); w.Code != 401 {
		t.Fatal("photo bypassed authentication")
	}
	if w := photoForm(t, protected, fields, nil, true); w.Code != 401 {
		t.Fatal("upload bypassed authentication")
	}
	page := request(h, "GET", "/producer/djs/default-pop", nil, nil).Body.String()
	if strings.Contains(page, `name="portrait_url"`) || !strings.Contains(page, `enctype="multipart/form-data"`) {
		t.Fatal("URL field remains or upload missing")
	}
}
