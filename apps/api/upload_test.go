package main

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"testing"
)

func TestLargeUploadSpillsAndCleansUp(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("TMPDIR", directory)
	var raw bytes.Buffer
	writer := multipart.NewWriter(&raw)
	if err := writer.WriteField("model", "policy:1"); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("file", "speech.wav")
	if err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("a"), (1<<20)+100)
	if _, err := file.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/v1/audio/transcriptions", &raw)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	input, err := readInference(httptest.NewRecorder(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer input.form.RemoveAll()
	files, err := os.ReadDir(directory)
	if err != nil || len(files) == 0 {
		t.Fatalf("large audio stayed in memory: %+v %v", files, err)
	}
	if input.size != int64(len(payload)) || len(input.fingerprint) != 64 {
		t.Fatal("upload size/fingerprint missing")
	}
	source, err := input.form.File["file"][0].Open()
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(source)
	source.Close()
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatal("spooled upload changed")
	}
	if err := input.form.RemoveAll(); err != nil {
		t.Fatal(err)
	}
	files, err = os.ReadDir(directory)
	if err != nil || len(files) != 0 {
		t.Fatalf("temporary audio retained: %+v %v", files, err)
	}
}
