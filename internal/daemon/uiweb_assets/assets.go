// Package uiweb_assets serves the daemon browser console's inert presentation
// assets. It deliberately owns no Manager authority: every fact and mutation
// still crosses the authenticated Go Manager API.
package uiweb_assets

import (
	"bytes"
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/operatorhelp"
	"github.com/vibe-agi/hideout/internal/packagekit"
)

//go:embed index.html style.css state.js client.js activity.js config.js presentation.js app.js
var files embed.FS

var indexTemplate = template.Must(
	template.New("index.html").Parse(mustAsset("index.html")),
)

type Options struct {
	AllowedHost   string
	AllowedOrigin string
	ExpiresAt     func() time.Time
	HelpCatalog   operatorhelp.Catalog
}

type Handler struct {
	allowedHost   string
	allowedOrigin string
	expiresAt     func() time.Time
	helpCatalog   operatorhelp.Catalog
}

// EmbeddedManifest reports the exact inert browser-console bytes compiled into
// the current container binary. Packaging binds this manifest to the finalized
// executable digest after signing, so a stale source-tree asset inventory
// cannot stand in for the bytes users receive.
func EmbeddedManifest(containerSHA256 string) (packagekit.EmbeddedAssetManifest, error) {
	assets := packagekit.BrowserConsoleAssets()
	for index := range assets {
		data, err := files.ReadFile(assets[index].Path)
		if err != nil {
			return packagekit.EmbeddedAssetManifest{}, err
		}
		assets[index].SHA256 = packagekit.BytesSHA256(data)
	}
	manifest := packagekit.EmbeddedAssetManifest{
		Schema:          packagekit.EmbeddedAssetManifestSchema,
		ID:              packagekit.BrowserConsoleAssetID,
		Container:       packagekit.BrowserConsoleContainerPath,
		ContainerSHA256: containerSHA256,
		License:         packagekit.BrowserConsoleAssetLicense,
		Assets:          assets,
	}
	if err := packagekit.ValidateEmbeddedAssetManifest(manifest); err != nil {
		return packagekit.EmbeddedAssetManifest{}, err
	}
	return manifest, nil
}

func NewHandler(options Options) http.Handler {
	catalog := options.HelpCatalog.Clone()
	if err := catalog.Validate(); err != nil {
		catalog = operatorhelp.Catalog{
			Schema:   operatorhelp.CatalogSchema,
			Commands: []operatorhelp.Command{},
		}
	}
	return Handler{
		allowedHost:   strings.TrimSpace(options.AllowedHost),
		allowedOrigin: strings.TrimSpace(options.AllowedOrigin),
		expiresAt:     options.ExpiresAt,
		helpCatalog:   catalog,
	}
}

func (handler Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w.Header())
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	if handler.allowedHost == "" || r.Host != handler.allowedHost {
		http.Error(w, "host is not allowed", http.StatusForbidden)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" &&
		origin != handler.allowedOrigin {
		http.Error(w, "origin is not allowed", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/":
		handler.serveIndex(w)
	case "/assets/style.css":
		serveAsset(w, "text/css; charset=utf-8", "style.css")
	case "/assets/state.js":
		serveAsset(w, "text/javascript; charset=utf-8", "state.js")
	case "/assets/client.js":
		serveAsset(w, "text/javascript; charset=utf-8", "client.js")
	case "/assets/activity.js":
		serveAsset(w, "text/javascript; charset=utf-8", "activity.js")
	case "/assets/config.js":
		serveAsset(w, "text/javascript; charset=utf-8", "config.js")
	case "/assets/presentation.js":
		serveAsset(w, "text/javascript; charset=utf-8", "presentation.js")
	case "/assets/app.js":
		serveAsset(w, "text/javascript; charset=utf-8", "app.js")
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (handler Handler) serveIndex(w http.ResponseWriter) {
	catalogJSON, err := json.Marshal(handler.helpCatalog)
	if err != nil {
		catalogJSON = []byte(
			`{"schema":"hideout.operator-help.v1","commands":[]}`,
		)
	}
	var expiresAt time.Time
	if handler.expiresAt != nil {
		expiresAt = handler.expiresAt().UTC()
	}
	data := struct {
		ExpiresAt   string
		HelpCatalog string
	}{
		ExpiresAt:   expiresAt.Format(time.RFC3339),
		HelpCatalog: string(catalogJSON),
	}
	var rendered bytes.Buffer
	if err := indexTemplate.Execute(&rendered, data); err != nil {
		http.Error(w, "browser console unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(rendered.Bytes())
}

func serveAsset(w http.ResponseWriter, contentType, name string) {
	data, err := files.ReadFile(name)
	if err != nil {
		http.Error(w, "asset unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}

func setSecurityHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Frame-Options", "DENY")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set(
		"Content-Security-Policy",
		"default-src 'none'; connect-src 'self'; img-src 'self' data:; "+
			"style-src 'self'; script-src 'self'; base-uri 'none'; "+
			"form-action 'none'; frame-ancestors 'none'",
	)
}

func mustAsset(name string) string {
	data, err := files.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return string(data)
}
