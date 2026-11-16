package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"

	"voice-cloud/db"
)

const inferenceBodyLimit = 32 << 20

type inferenceInput struct {
	task, model, language string
	stream                bool
	fields                map[string]json.RawMessage
	form                  *multipart.Form
	raw                   []byte
}

func readInference(w http.ResponseWriter, r *http.Request) (*inferenceInput, error) {
	in := &inferenceInput{task: "llm"}
	switch r.URL.Path {
	case "/v1/audio/speech":
		in.task = "tts"
	case "/v1/audio/transcriptions":
		in.task = "stt"
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, inferenceBodyLimit))
	if err != nil {
		return nil, decodeError(err)
	}
	in.raw = raw
	media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return nil, httpError{415, "invalid Content-Type"}
	}
	if in.task == "stt" {
		if media != "multipart/form-data" || params["boundary"] == "" {
			return nil, httpError{415, "transcriptions require multipart/form-data"}
		}
		in.form, err = multipart.NewReader(bytes.NewReader(raw), params["boundary"]).ReadForm(inferenceBodyLimit)
		if err != nil {
			return nil, invalid("invalid multipart body")
		}
		// The complete request fits in memory; always clean up in case parsing spills a file.
		if len(in.form.File) != 1 || len(in.form.File["file"]) != 1 || in.form.File["file"][0].Size == 0 {
			in.form.RemoveAll()
			return nil, invalid("one nonempty audio file is required")
		}
		for _, name := range []string{"model", "language", "stream"} {
			if len(in.form.Value[name]) > 1 {
				in.form.RemoveAll()
				return nil, invalid("duplicate routing field")
			}
		}
		in.model = firstForm(in.form, "model")
		in.language = firstForm(in.form, "language")
		stream := firstForm(in.form, "stream")
		if stream != "" && stream != "true" && stream != "false" {
			in.form.RemoveAll()
			return nil, invalid("stream must be true or false")
		}
		in.stream = stream == "true"
	} else {
		if media != "application/json" {
			return nil, httpError{415, "Content-Type must be application/json"}
		}
		if err := json.Unmarshal(raw, &in.fields); err != nil || in.fields == nil {
			return nil, invalid("body must be a JSON object")
		}
		for name, dest := range map[string]*string{"model": &in.model, "language": &in.language} {
			if value, ok := in.fields[name]; ok {
				if string(value) == "null" || json.Unmarshal(value, dest) != nil {
					return nil, invalid(name + " must be a string")
				}
			}
		}
		if value, ok := in.fields["stream"]; ok {
			if string(value) == "null" || json.Unmarshal(value, &in.stream) != nil {
				return nil, invalid("stream must be a boolean")
			}
		}
		if in.task == "llm" {
			var messages []json.RawMessage
			if json.Unmarshal(in.fields["messages"], &messages) != nil || len(messages) == 0 {
				return nil, invalid("messages must be a nonempty array")
			}
		} else {
			for _, name := range []string{"input", "voice"} {
				var value string
				if json.Unmarshal(in.fields[name], &value) != nil || strings.TrimSpace(value) == "" {
					return nil, invalid(name + " must be a nonempty string")
				}
			}
		}
	}
	if in.language != "" {
		if len(in.language) > 64 || strings.ContainsAny(in.language, " \t\r\n_") {
			if in.form != nil {
				in.form.RemoveAll()
			}
			return nil, invalid("language must be a language tag")
		}
	}
	return in, nil
}

func firstForm(form *multipart.Form, name string) string {
	if len(form.Value[name]) == 0 {
		return ""
	}
	return form.Value[name][0]
}

func (in *inferenceInput) upstreamBody(d db.Deployment) ([]byte, string, error) {
	if in.form != nil {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for name, values := range in.form.Value {
			if name == "model" {
				continue
			}
			for _, value := range values {
				if err := writer.WriteField(name, value); err != nil {
					return nil, "", err
				}
			}
		}
		if err := writer.WriteField("model", d.UpstreamModelID); err != nil {
			return nil, "", err
		}
		file := in.form.File["file"][0]
		source, err := file.Open()
		if err != nil {
			return nil, "", err
		}
		defer source.Close()
		header := make(textproto.MIMEHeader)
		header.Set("Content-Type", file.Header.Get("Content-Type"))
		header.Set("Content-Disposition", mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": file.Filename}))
		part, err := writer.CreatePart(header)
		if err != nil {
			return nil, "", err
		}
		if _, err = io.Copy(part, source); err != nil {
			return nil, "", err
		}
		if err = writer.Close(); err != nil {
			return nil, "", err
		}
		return body.Bytes(), writer.FormDataContentType(), nil
	}
	fields := make(map[string]json.RawMessage, len(in.fields)+1)
	for key, value := range in.fields {
		fields[key] = value
	}
	fields["model"], _ = json.Marshal(d.UpstreamModelID)
	// Routing language is forwarded only where the compatible protocol supports it.
	if in.task == "llm" {
		delete(fields, "language")
	}
	body, err := json.Marshal(fields)
	return body, "application/json", err
}
