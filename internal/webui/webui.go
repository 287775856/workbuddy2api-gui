// Package webui 把前端构建产物（web/dist）以 embed 方式打包进二进制。
//
// 为什么用 embed 而不是依赖外部目录：GUI 要作为单文件/单容器交付，
// 产物里没有 Node 运行时也不影响运行。
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// distFS 前端构建产物。构建前 dist 下只有占位文件，保证 go build 永远可用。
//
//go:embed all:dist
var distFS embed.FS

// Handler 返回 SPA 静态资源 handler：
//   - 命中真实文件 → 直接返回（带长缓存，文件名含内容哈希时）
//   - 其余路径     → 返回 index.html（前端路由接管）
func Handler() (http.Handler, error) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	fileServer := http.FileServer(http.FS(sub))
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		// 前端未构建（只有占位文件）：返回可读的提示页而不是 404 迷宫。
		return http.HandlerFunc(notBuilt), nil
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
		if clean == "/" {
			serveIndex(w, index)
			return
		}
		// 静态资源存在则交给 FileServer。
		if f, err := sub.Open(strings.TrimPrefix(clean, "/")); err == nil {
			_ = f.Close()
			if strings.HasPrefix(clean, "/assets/") {
				// Vite 产物文件名带内容哈希，可安全长缓存。
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		// 其余一律回落 index.html（前端路由 / 深链接刷新）。
		serveIndex(w, index)
	}), nil
}

func serveIndex(w http.ResponseWriter, index []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// index.html 绝不能缓存：否则前端发版后用户拿到旧 HTML 引用不存在的 assets。
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	_, _ = w.Write(index)
}

// notBuilt 前端未构建时的提示页。
func notBuilt(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8">
<title>WorkBuddy GUI · 前端未构建</title>
<style>
body{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;background:#0f1117;color:#e6e8ee;
display:flex;align-items:center;justify-content:center;min-height:100vh;margin:0;padding:24px}
.card{max-width:640px;background:#171a23;border:1px solid #262b38;border-radius:12px;padding:28px}
h1{font-size:19px;margin:0 0 12px}code{background:#0b0d13;padding:2px 6px;border-radius:5px;color:#7ee787}
pre{background:#0b0d13;padding:12px;border-radius:8px;overflow:auto;color:#7ee787;font-size:13px}
p{line-height:1.7;color:#a9b0c0;font-size:14px}
</style></head><body><div class="card">
<h1>前端产物尚未构建</h1>
<p>后端已正常运行，但二进制内没有打包前端资源。请在项目根目录执行：</p>
<pre>cd web &amp;&amp; npm install &amp;&amp; npm run build
cd .. &amp;&amp; go build -o wbgui ./cmd/server</pre>
<p>然后重新启动本服务。API 接口此时已可用，例如 <code>/api/session</code>。</p>
</div></body></html>`))
}
