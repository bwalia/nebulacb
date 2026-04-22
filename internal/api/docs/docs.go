// Package docs embeds the OpenAPI 3.0 spec and serves Swagger UI.
package docs

import (
	_ "embed"
	"net/http"
)

//go:embed openapi.yaml
var openAPISpec []byte

// Spec returns the raw OpenAPI 3.0 YAML document.
func Spec() []byte { return openAPISpec }

// HandleSpec serves openapi.yaml at its mounted path.
func HandleSpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	_, _ = w.Write(openAPISpec)
}

// HandleUI serves a minimal Swagger UI page that loads the embedded spec.
// Swagger UI assets are pulled from the public CDN so no extra deps are required.
func HandleUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerUIHTML))
}

const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <title>NebulaCB API — Swagger UI</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui.css" />
  <link rel="icon" type="image/svg+xml" href="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16'%3E%3Ccircle cx='8' cy='8' r='7' fill='%231976d2'/%3E%3C/svg%3E" />
  <style>
    body { margin: 0; background: #fafafa; }
    .topbar { display: none; }
  </style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
  <script>
    window.ui = SwaggerUIBundle({
      url: "/api/v1/openapi.yaml",
      dom_id: "#swagger-ui",
      deepLinking: true,
      persistAuthorization: true,
      tryItOutEnabled: true,
      docExpansion: "list",
      filter: true,
      presets: [SwaggerUIBundle.presets.apis]
    });
  </script>
</body>
</html>
`
