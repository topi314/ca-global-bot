package server

import (
	"bytes"
	"embed"
	htmltemplate "html/template"
	"log/slog"
	"net/http"
	texttemplate "text/template"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

var (
	htmlPage  = htmltemplate.Must(htmltemplate.ParseFS(templatesFS, "templates/page.html.tmpl"))
	textTmpls = texttemplate.Must(texttemplate.ParseFS(templatesFS, "templates/messages.txt.tmpl"))
)

type pageData struct {
	Title string
	Body  string
}

// renderText renders a named static text template from messages.txt.tmpl.
func renderText(name string) string {
	var b bytes.Buffer
	if err := textTmpls.ExecuteTemplate(&b, name, nil); err != nil {
		slog.Error("failed to render text template", slog.String("name", name), slog.Any("err", err))
		return ""
	}
	return b.String()
}

func writeHTML(w http.ResponseWriter, status int, title, body string) {
	var b bytes.Buffer
	if err := htmlPage.ExecuteTemplate(&b, "page", pageData{Title: title, Body: body}); err != nil {
		slog.Error("failed to render html page", slog.Any("err", err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
}
