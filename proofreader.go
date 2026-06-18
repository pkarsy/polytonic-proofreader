// Polytonic Proofreader
//
// A single-file browser-based OCR proofreader.
// Compare scanned page images side-by-side with their OCR text,
// correct errors, and save — all from a browser tab.
//
// Originally created for proofreading "ΤΟ ΣΥΜΠΑΝ" (Smyrna, 1888)
// polytonic Greek OCR, but designed to be forked for any script.
//
// ── Architecture ──────────────────────────────────────────────────
//
// This entire application is ONE Go file containing:
//
//   [Go server]  ~lines 89–298   HTTP server, file discovery, API
//   [Frontend]   ~lines 300–1049 Entire HTML/CSS/JS embedded in
//                                the `indexHTML` const (Go string)
//   [main()]     ~lines 1053–1087 Entry point, route registration
//
// ── What is script-generic (no changes needed for a fork) ──────
//
//   • Go HTTP server — serves images, text files, saves edits
//   • File discovery — scans* dirs (images) + ocr/ dir (text)
//   • Side-by-side layout — split-pane image + text editor
//   • Zoom/pan on image — scrollwheel, click-drag, fit-width
//   • Edit/view mode toggle — textarea read-only vs. editable
//   • Undo/redo — native browser (Ctrl+Z/Y)
//   • Unsaved-changes tracking — * in tab title, beforeunload
//   • Viewport persistence — remembers zoom/scroll per page
//   • Hot-restart — /restart recompiles and replaces the server
//   • Save warnings — length-drop detection, text-type mismatch
//   • Line numbering — skips metadata lines like [header]
//   • Page grouping — images sharing a 3-char prefix are variants
//
// ── What is script-specific (replace for another script) ───────
//
//   Inside the embedded frontend (indexHTML, ~lines 300–960):
//
//   1. Character palette (~line 537)
//      The `greekCharGroups` array. Replace each group with your
//      script's characters + diacritics. Each row: a base letter
//      button (leftmost) followed by its accented/diacritic variants.
//
//   2. Transliteration map (~line 411)
//      The `latinToGreek` object. Map Latin QWERTY keys to your
//      script's base letters so users can type without switching
//      keyboard layouts. Set `forceGreek` default to false if
//      your script doesn't need it.
//
//   3. Text-type detection (~line 841)
//      The `detectTextType()` function. Currently classifies text
//      as polytonic Greek / neo-hellenic / English using Unicode
//      ranges. Replace the ranges with your script's blocks.
//
//   4. Accent-ignoring search (~line 921)
//      The `doFind()` function. Currently uses NFD normalization
//      (strips combining marks) plus Greek-specific phonetic
//      equivalence (ο↔ω, η↔ι↔υ). Keep the NFD approach for any
//      script with combining diacritics; replace the phonetic map.
//
//   5. Save-warning type checks (~line 866)
//      The expected-text-type logic in `checkSaveWarnings()`.
//      Update the type labels and expected dirs for your fork.
//
// ── How to fork for another script ─────────────────────────────
//
//   1. Copy proofreader.go → your-fork.go
//   2. Replace the `indexHTML` constant with a new frontend
//      containing your script's character palette, transliteration
//      map, and text detection. Keep the same HTML element IDs,
//      CSS classes, and JS function names — the Go server depends
//      on them being present.
//   3. Update the localStorage prefix (search for "proofreader"
//      in the JS — replace `proofreader` with your own prefix).
//   4. Rename the Go file, update findGoSource(), and you're done.
//
// ── Run ──────────────────────────────────────────────────────────
//
//   go run proofreader.go /path/to/project
//
//   Expects:
//     project/
//       scans/      page images (jpg, png, webp, gif, tif)
//         011.jpg
//       ocr/        OCR text files (auto-created)
//         011.txt
//
//   Open: http://localhost:1888/

package main

import (
        "encoding/json"
        "fmt"
        "html/template"
        "log"
        "mime"
        "net/http"
        "os"
        "os/exec"
        "path/filepath"
        "sort"
        "strconv"
        "strings"
        "syscall"
        "time"
)

// ──── Types ────


type App struct {
        ProjectDir  string
        ScanDirs    []string
        TextDirs    []string
        Pages       []string
        pageSources map[string][]int
        restartCmd  []string
        SourcePath  string // absolute path to proofreader.go
}

type PageEntry struct {
        Name    string `json:"name"`
        Sources []int  `json:"sources"`
}
type ListPayload struct {
        ScanDirs []string    `json:"scanDirs"`
        TextDirs []string    `json:"textDirs"`
        Pages    []PageEntry `json:"pages"`
}

type PagePayload struct {
        Page  string `json:"page"`
        Text  string `json:"text"`
        Index int    `json:"index"`
        Total int    `json:"total"`
}

type SavePayload struct {
        Page   string `json:"page"`
        Text   string `json:"text"`
        Source int    `json:"source"`
}

// ──── Helpers ────

func isImage(name string) bool {
        switch strings.ToLower(filepath.Ext(name)) {
        case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".tif", ".tiff":
                return true
        default:
                return false
        }
}

// ──── App initialization ────

func NewApp(projectDir string) (*App, error) {
        projectDir, err := filepath.Abs(projectDir)
        if err != nil { return nil, err }
        app := &App{ProjectDir: projectDir}

        // Discover all scans* directories (no subdirectories inside them)
        entries, err := os.ReadDir(projectDir)
        if err != nil { return nil, fmt.Errorf("cannot read project dir: %w", err) }
        app.pageSources = make(map[string][]int)
        pageOrder := make(map[string]int)
        for _, e := range entries {
                if !e.IsDir() || !strings.HasPrefix(e.Name(), "scans") { continue }
                app.ScanDirs = append(app.ScanDirs, e.Name())
        }
        sort.Strings(app.ScanDirs)
        // scans comes first (it's the primary folder)
        for i, d := range app.ScanDirs {
                if d == "scans" && i > 0 {
                        app.ScanDirs[0], app.ScanDirs[i] = app.ScanDirs[i], app.ScanDirs[0]
                        break
                }
        }
        for si, dir := range app.ScanDirs {
                full := filepath.Join(projectDir, dir)
                fentries, err := os.ReadDir(full)
                if err != nil { continue }
                for _, fe := range fentries {
                        if fe.IsDir() || !isImage(fe.Name()) { continue }
                        app.pageSources[fe.Name()] = append(app.pageSources[fe.Name()], si)
                        if _, seen := pageOrder[fe.Name()]; !seen {
                                pageOrder[fe.Name()] = len(app.Pages)
                                app.Pages = append(app.Pages, fe.Name())
                        }
                }
        }
        sort.Strings(app.Pages)

        if len(app.ScanDirs) == 0 {
                return nil, fmt.Errorf("no 'scans*' directory found in %s -- create a folder named 'scans' with page images", app.ProjectDir)
        }
        if len(app.Pages) == 0 {
                return nil, fmt.Errorf("no image files found in %v -- place at least one .jpg/.png/.webp/.gif/.tif file inside a 'scans' folder", app.ScanDirs)
        }

        // Auto-create missing text directory (ocr or el)
        for _, name := range []string{"ocr", "el"} {
                full := filepath.Join(projectDir, name)
                if info, err := os.Stat(full); err == nil && info.IsDir() {
                        app.TextDirs = append(app.TextDirs, name)
                }
        }
        if len(app.TextDirs) == 0 {
                // Neither exists — create ocr/ as default
                ocrDir := filepath.Join(projectDir, "ocr")
                if err := os.MkdirAll(ocrDir, 0755); err != nil {
                        return nil, fmt.Errorf("cannot create 'ocr' directory: %w", err)
                }
                app.TextDirs = append(app.TextDirs, "ocr")
                fmt.Printf("Created missing 'ocr' directory: %s\n", ocrDir)
        }
        return app, nil
}

// ──── Page/text path helpers ────

func (a *App) pageIndex(page string) int { for i, p := range a.Pages { if p == page { return i } }; return -1 }
func (a *App) txtPath(page string, srcIdx int) string {
        if srcIdx < 0 || srcIdx >= len(a.TextDirs) { srcIdx = 0 }
        return filepath.Join(a.ProjectDir, a.TextDirs[srcIdx], strings.TrimSuffix(page, filepath.Ext(page))+".txt")
}

// ──── HTTP handlers ────

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
        t := template.Must(template.New("index").Parse(indexHTML))
        avail := a.restartCmd != nil
        if err := t.Execute(w, map[string]bool{"RestartAvailable": avail}); err != nil { http.Error(w, err.Error(), 500) }
}
func (a *App) handleList(w http.ResponseWriter, r *http.Request) {
        entries := make([]PageEntry, len(a.Pages))
        for i, p := range a.Pages {
                entries[i] = PageEntry{Name: p, Sources: a.pageSources[p]}
        }
        writeJSON(w, ListPayload{ScanDirs: a.ScanDirs, TextDirs: a.TextDirs, Pages: entries})
}
func (a *App) handleImage(w http.ResponseWriter, r *http.Request) {
        page := r.PathValue("page")
        if a.pageIndex(page) < 0 { http.NotFound(w, r); return }
        srcIdx := 0
        if s := r.URL.Query().Get("source"); s != "" {
                if v, err := strconv.Atoi(s); err == nil { srcIdx = v }
        }
        if srcIdx < 0 || srcIdx >= len(a.ScanDirs) { srcIdx = 0 }
        dir := a.ScanDirs[srcIdx]
        path := filepath.Join(a.ProjectDir, dir, page)
        if mt := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); mt != "" { w.Header().Set("Content-Type", mt) }
        http.ServeFile(w, r, path)
}
func (a *App) handleText(w http.ResponseWriter, r *http.Request) {
        page := r.URL.Query().Get("page")
        if page == "" && len(a.Pages) > 0 { page = a.Pages[0] }
        idx := a.pageIndex(page)
        if idx < 0 { http.Error(w, "unknown page", 404); return }
        srcIdx := 0
        if s := r.URL.Query().Get("source"); s != "" {
                if v, err := strconv.Atoi(s); err == nil { srcIdx = v }
        }
        txtPath := a.txtPath(page, srcIdx)
        textBytes, err := os.ReadFile(txtPath)
        if os.IsNotExist(err) {
                textBytes = []byte("")
                if err := os.WriteFile(txtPath, textBytes, 0644); err != nil { http.Error(w, err.Error(), 500); return }
        } else if err != nil { http.Error(w, err.Error(), 500); return }
        writeJSON(w, PagePayload{Page: page, Text: string(textBytes), Index: idx, Total: len(a.Pages)})
}
func (a *App) handleSave(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost { http.Error(w, "POST required", 405); return }
        var payload SavePayload
        if err := json.NewDecoder(r.Body).Decode(&payload); err != nil { http.Error(w, err.Error(), 400); return }
        if a.pageIndex(payload.Page) < 0 { http.Error(w, "unknown page", 404); return }
        normalized := strings.ReplaceAll(payload.Text, "\u00b7", "\u0387")
        txtPath := a.txtPath(payload.Page, payload.Source)
        if err := os.WriteFile(txtPath, []byte(normalized), 0644); err != nil { http.Error(w, err.Error(), 500); return }
        writeJSON(w, map[string]string{"status": "ok", "savedLength": strconv.Itoa(len(normalized))})
}
func writeJSON(w http.ResponseWriter, v any) { w.Header().Set("Content-Type", "application/json; charset=utf-8"); _ = json.NewEncoder(w).Encode(v) }

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	page := r.URL.Query().Get("page")
	if page == "" {
		http.Error(w, "page required", 400)
		return
	}
	srcIdx := 0
	if s := r.URL.Query().Get("source"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			srcIdx = v
		}
	}
	textSrcIdx := 0
	if s := r.URL.Query().Get("textSource"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			textSrcIdx = v
		}
	}

	// Resolve the image and text paths
	var imagePath string
	if srcIdx >= 0 && srcIdx < len(a.ScanDirs) {
		imagePath = filepath.Join(a.ProjectDir, a.ScanDirs[srcIdx], page)
	}
	var textPath string
	if textSrcIdx >= 0 && textSrcIdx < len(a.TextDirs) && a.pageIndex(page) >= 0 {
		textPath = a.txtPath(page, textSrcIdx)
	}

	// SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 400)
		return
	}

	// Last-known file info
	type fileStat struct {
		modTime time.Time
		size    int64
	}
	var lastImage, lastText *fileStat
	if imagePath != "" {
		if fi, err := os.Stat(imagePath); err == nil {
			lastImage = &fileStat{modTime: fi.ModTime(), size: fi.Size()}
		}
	}
	if textPath != "" {
		if fi, err := os.Stat(textPath); err == nil {
			lastText = &fileStat{modTime: fi.ModTime(), size: fi.Size()}
		}
	}

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if imagePath != "" {
				if fi, err := os.Stat(imagePath); err == nil {
					cur := &fileStat{modTime: fi.ModTime(), size: fi.Size()}
					if lastImage == nil || !cur.modTime.Equal(lastImage.modTime) || cur.size != lastImage.size {
						lastImage = cur
						fmt.Fprintf(w, "event: image-changed\ndata: %d\n\n", cur.modTime.UnixMilli())
						flusher.Flush()
					}
				} else {
					lastImage = nil // file disappeared
				}
			}
			if textPath != "" {
				if fi, err := os.Stat(textPath); err == nil {
					cur := &fileStat{modTime: fi.ModTime(), size: fi.Size()}
					if lastText == nil || !cur.modTime.Equal(lastText.modTime) || cur.size != lastText.size {
						lastText = cur
						fmt.Fprintf(w, "event: text-changed\ndata: %d\n\n", cur.modTime.UnixMilli())
						flusher.Flush()
					}
				} else {
					lastText = nil
				}
			}
		}
	}
}

func (a *App) handleRestart(w http.ResponseWriter, r *http.Request) {
        if a.restartCmd == nil {
                w.Header().Set("Content-Type", "text/html; charset=utf-8")
                fmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><meta http-equiv="refresh" content="2;url=/"><title>Restart unavailable</title><style>body{font-family:system-ui,sans-serif;padding:3em;text-align:center;background:#1e1e1e;color:#f0f0f0}h1{color:#e74c3c}</style></head><body><h1>Restart unavailable</h1><p>The Go binary or source file was not found when the server started.</p><p>Make sure <code>go</code> is installed and <code>proofreader.go</code> is present.</p><p>Returning to the editor…</p></body></html>`)
                return
        }
        log.Printf("[%s] restart triggered via %s", time.Now().Format(time.RFC3339), r.RemoteAddr)
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        fmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><meta http-equiv="refresh" content="1;url=/"><title>Restarting</title><style>body{font-family:system-ui,sans-serif;padding:3em;text-align:center;background:#1e1e1e;color:#f0f0f0}</style></head><body><h1>Restarting…</h1><p>The server will reload in 1 second.</p></body></html>`)
        if f, ok := w.(http.Flusher); ok {
                f.Flush()
        }
        go func() {
                time.Sleep(200 * time.Millisecond)
                log.Printf("[%s] executing: %v", time.Now().Format(time.RFC3339), a.restartCmd)
                syscall.Exec(a.restartCmd[0], a.restartCmd, os.Environ())
        }()
}

// ──── Restart helper ────

func findGoSource(projectDir string) string {
        // Try current working directory first
        if fp, err := filepath.Abs("proofreader.go"); err == nil {
                if _, err := os.Stat(fp); err == nil {
                        return fp
                }
        }
        // Try parent of the absolute project directory
        candidate := filepath.Join(projectDir, "..", "proofreader.go")
        if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
                return candidate
        }
        return ""
}

// ──── Embedded frontend (HTML/CSS/JS) ────

const indexHTML = `<!doctype html>
<html lang="el">
<head>
<meta charset="utf-8">
<title>Polytonic Proofreader</title>
<style>
  :root { --bg:#1e1e1e; --panel:#2b2b2b; --text:#f0f0f0; --muted:#b0b0b0; --border:#555; }
  * { box-sizing: border-box; }
  body { margin:0; height:100vh; overflow:hidden; background:var(--bg); color:var(--text); font-family:system-ui,sans-serif; }
  #toolbar { height:46px; display:flex; align-items:center; gap:8px; padding:6px 10px; background:#111; border-bottom:1px solid var(--border); }
  button, select { font-size:16px; padding:5px 10px; }
  #status { color:var(--muted); margin-left:8px; white-space:nowrap; min-width:22ch; display:inline-block; text-align:left; }
  #main { height:calc(100vh - 46px); display:grid; grid-template-columns:50% 50%; }
  #left,#right { min-width:0; min-height:0; }
  #left { background:#333; overflow:hidden; border-right:1px solid var(--border); position:relative; cursor:grab; }
  #left.dragging { cursor:grabbing; }
  #left.mag-active { cursor:none; }
  #left.dragging .magnifier { display:none !important; }
  .magnifier { display:none; position:absolute; width:160px; height:120px; border:2px solid rgba(255,255,255,0.7); box-shadow:0 0 12px rgba(0,0,0,0.5); pointer-events:none; z-index:10; background-repeat:no-repeat; }
  #scan { position:absolute; left:0; top:0; transform-origin:top left; max-width:none; user-select:none; -webkit-user-drag:none; }
  #right { display:flex; flex-direction:row; background:var(--panel); }
  #right.hide-linenums #lineNumbers { display:none; }
  #lineNumbers { padding:0 4px 250px 8px; text-align:right; background:#e0dfce; color:#888; font-family:"GFS Didot","DejaVu Serif",serif; font-size:20px; line-height:1.35; overflow:hidden; user-select:none; min-width:2ch; border-right:1px solid #bbb; }
  #editorWrap { flex:1; display:flex; position:relative; }
  #highlightOverlay { position:absolute; inset:0; padding:0 14px 250px; pointer-events:none; overflow:hidden; font-family:"GFS Didot","DejaVu Serif",serif; font-size:20px; line-height:1.35; white-space:pre; color:transparent; }
  #highlightOverlay mark { background:rgba(100,255,100,0.45); color:transparent; }
  textarea { flex:1; resize:none; border:none; outline:none; padding:0 14px 250px; background:#fbfbf2; color:#111; font-family:"GFS Didot","DejaVu Serif",serif; font-size:20px; line-height:1.35; white-space:pre; overflow:auto; }

  #right.edit-off textarea { cursor:default; background:#efeee4; }

  /* Warn modal */
  #warnModal { display:none; position:fixed; inset:0; background:rgba(0,0,0,0.5); z-index:1000; align-items:center; justify-content:center; }
  #warnModal.show { display:flex; }
  #warnModal .box { background:#2b2b2b; padding:24px; border-radius:8px; min-width:300px; color:var(--text); font-family:system-ui,sans-serif; }
  #warnModal p { margin:0 0 16px; font-size:15px; }
  #warnModal .btns { display:flex; gap:8px; justify-content:flex-end; }
  #warnModal button { min-width:70px; }
  #saveWarnModal { display:none; position:fixed; inset:0; background:rgba(0,0,0,0.5); z-index:1000; align-items:center; justify-content:center; }
  #saveWarnModal.show { display:flex; }
  #saveWarnModal .box { background:#2b2b2b; padding:24px; border-radius:8px; min-width:400px; max-width:600px; color:var(--text); font-family:system-ui,sans-serif; }
  #saveWarnModal p { margin:0 0 16px; font-size:15px; line-height:1.4; }
  #saveWarnModal .btns { display:flex; gap:8px; justify-content:flex-end; }
  #saveWarnModal button { min-width:70px; }
  /* Settings modal */
  #settingsModal { display:none; position:fixed; inset:0; background:rgba(0,0,0,0.5); z-index:1000; align-items:center; justify-content:center; }
  #settingsModal.show { display:flex; }
  #settingsModal .box { background:#2b2b2b; padding:24px; border-radius:8px; min-width:260px; color:var(--text); font-family:system-ui,sans-serif; }
  #settingsModal label { font-size:15px; user-select:none; }
  #settingsModal .btns { display:flex; gap:8px; justify-content:flex-end; }
  /* Greek character palette */
  #greekPalette { display:none; position:fixed; top:50%; left:50%; transform:translate(-50%,-50%); z-index:999; background:#2b2b2b; padding:12px; border-radius:8px; box-shadow:0 4px 24px rgba(0,0,0,0.6); max-height:80vh; overflow-y:auto; }
  #greekPalette.show { display:block; }
  #greekPalette .row { display:flex; align-items:center; margin:3px 0; }
  #greekPalette .label { width:32px; text-align:center; font-size:24px; color:#b0b0b0; font-weight:bold; flex-shrink:0; cursor:pointer; background:none; border:none; padding:0; }
  #greekPalette .chars { display:flex; flex-wrap:wrap; gap:2px; }
  #greekPalette button { font-size:24px; width:42px; height:42px; padding:0; border:none; border-radius:4px; cursor:pointer; background:#3b3b3b; color:var(--text); display:flex; align-items:center; justify-content:center; font-family:"GFS Didot","DejaVu Serif",serif; }
  #greekPalette button:hover { background:#555; }
</style>
</head>
<body>
  <div id="toolbar">
    <button onclick="prevPage()">← Previous</button>
    <button onclick="nextPage()">Next →</button>
    <button onclick="zoomOutCenter()">Zoom -</button>
    <button onclick="zoomInCenter()">Zoom +</button>
    <button onclick="fitWidth()">Fit width</button>

    <select id="pageSelect" style="font-size:14px;width:12ch" onchange="loadPage(parseInt(this.value))"></select>
    <select id="sourceSelect" style="font-size:14px" onchange="switchSource(this.value)"></select>
    <select id="textSourceSelect" style="font-size:14px" onchange="switchTextSource(this.value)"></select>
    <span style="margin-left:auto;display:flex;align-items:center;gap:6px">
      <span style="display:inline-flex;align-items:center;background:#fbfbf2;border-radius:4px;padding:0 4px"><input type="text" id="findInput" style="width:110px;font-size:16px;padding:3px 4px;border:none;background:transparent;outline:none" placeholder="Find..." oninput="doFind()"><button onclick="document.getElementById('findInput').value='';doFind();document.getElementById('findInput').focus()" style="background:none;border:none;cursor:pointer;font-size:16px;padding:0 2px;color:#555;cursor:pointer">✕</button></span>
      <span id="findCount" style="color:var(--muted);font-size:18px;min-width:36px;font-weight:bold">0</span>
      <button onclick="saveText()" style="font-size:14px;padding:4px 8px">Save</button>
      <button onclick="copyAll()" style="font-size:14px;padding:4px 8px;margin-right:4px">Copy All</button>
      <button id="grCharBtn" onclick="toggleGreekPalette()" style="font-size:14px;padding:4px 8px;margin-right:4px" title="Polytonic Greek characters (Ctrl+P)">Greek Char</button>
      <button onclick="toggleSettings()" style="font-size:14px;padding:4px 8px;margin-right:4px" title="Settings">Settings</button>
      <button id="restartBtn" onclick="window.location.href='/restart'" style="font-size:14px;padding:4px 8px;margin-right:4px;background:#8b0000;color:#fff" title="Restart server (recompile)">Restart</button>
      <label style="color:var(--text);font-size:14px;user-select:none;display:flex;align-items:center;gap:4px">
        <input type="checkbox" id="editToggle" onchange="toggleEditMode()"> Edit
      </label>
    </span>
    <span id="status"></span>
  </div>
  <div id="warnModal">
    <div class="box">
      <p>Unsaved changes — what would you like to do?</p>
      <div class="btns">
        <button id="warnSave">Save &amp; continue</button>
        <button id="warnDiscard">Discard</button>
        <button id="warnCancel">Cancel</button>
      </div>
    </div>
  </div>
  <div id="saveWarnModal">
    <div class="box">
      <p id="saveWarnMsg"></p>
      <div class="btns">
        <button id="saveWarnContinue" style="background:#8b0000;color:#fff">Save anyway</button>
        <button id="saveWarnCancel">Cancel</button>
      </div>
    </div>
  </div>
  <div id="greekPalette"></div>
  <div id="settingsModal">
    <div class="box">
      <p style="font-size:16px;font-weight:bold;margin:0 0 12px">Settings</p>
      <label style="display:flex;align-items:center;gap:8px;margin:6px 0;cursor:pointer">
        <input type="checkbox" id="showLineNums" onchange="toggleLineNumbers()" checked> Lines
      </label>
      <label style="display:flex;align-items:center;gap:8px;margin:6px 0;cursor:pointer">
        <input type="checkbox" id="magToggle" onchange="toggleMagnifier()"> Always-on magnifier
      </label>
      <label style="display:flex;align-items:center;gap:8px;margin:6px 0;cursor:pointer">
        <input type="checkbox" id="forceGreekToggle" onchange="toggleForceGreek()" checked> Force Greek
      </label>
      <label style="display:flex;align-items:center;gap:8px;margin:6px 0;cursor:pointer">
        <input type="checkbox" id="digraphToggle" onchange="toggleDigraph()" checked> Digraph matching
      </label>
      <div class="btns" style="margin-top:12px"><button onclick="toggleSettings()">Close</button></div>
    </div>
  </div>
  <div id="main">
    <div id="left"><img id="scan" draggable="false"><div class="magnifier" id="magnifier"></div></div>
    <div id="right"><div id="lineNumbers"></div><div id="editorWrap"><div id="highlightOverlay"></div><textarea id="editor" spellcheck="false"></textarea></div></div>
  </div>
<script>
let scanDirs=[], pages=[], page="", pageIndex=0, sourceIndex=0, textDirs=[], textSourceIndex=0, zoom=1.0, modified=false;
let savedText="";
let eventSource=null;
let currentTitlePage="";
const latinToGreek={'a':'α','b':'β','c':'ψ','d':'δ','e':'ε','f':'φ','g':'γ','h':'η','i':'ι','j':'ξ','k':'κ','l':'λ','m':'μ','n':'ν','o':'ο','p':'π','q':'ς','r':'ρ','s':'σ','t':'τ','u':'θ','v':'ω','x':'χ','y':'υ','z':'ζ'};
let forceGreek = localStorage.getItem('proofreaderForceGreek') !== '0';
let digraphMatch = localStorage.getItem('proofreaderDigraph') !== '0';
let editorFontSize = Number(localStorage.getItem("proofreaderEditorFontSize") || "20");

// Text pane drag-scroll state
let textDragging = false;
let textMoved = false;
let textStartX = 0;
let textStartY = 0;
let textScrollLeft = 0;
let textScrollTop = 0;

let imgX=0, imgY=0;
let dragging=false, dragStartX=0, dragStartY=0, dragImgX=0, dragImgY=0;
let findTerm="", findMatches=[], findIndex=0;
const scan=document.getElementById("scan"), editor=document.getElementById("editor"), statusEl=document.getElementById("status"), pageEl=document.getElementById("pageSelect"), sourceEl=document.getElementById("sourceSelect"), left=document.getElementById("left");

editor.addEventListener("input",()=>{
  modified = (editor.value !== savedText);
  updateTitle();
  setStatus(modified ? "modified" : "");
  updateLineNumbers();
});

left.addEventListener("wheel",(e)=>{
  if(e.ctrlKey||e.metaKey){ e.preventDefault(); if(!scan.naturalWidth) return; zoomAt(e.clientX,e.clientY,e.deltaY<0?1.05:1/1.05); }
  else{ e.preventDefault(); imgY-=e.deltaY; applyTransform(); }
},{passive:false});
left.addEventListener("mousedown",(e)=>{ if(e.button!==0) return; dragging=true; left.classList.add("dragging"); dragStartX=e.clientX; dragStartY=e.clientY; dragImgX=imgX; dragImgY=imgY; e.preventDefault(); });
window.addEventListener("mousemove",(e)=>{ if(!dragging) return; imgX=dragImgX+(e.clientX-dragStartX); imgY=dragImgY+(e.clientY-dragStartY); applyTransform(); });
window.addEventListener("mouseup",()=>{ dragging=false; left.classList.remove("dragging"); });
left.addEventListener("dblclick",(e)=>{ e.preventDefault(); if(Math.abs(zoom-1.0)<0.05) fitWidth(); else zoomAt(e.clientX,e.clientY,1.0/zoom); });

// ──── Magnifier ────
const mag = document.getElementById('magnifier');
const MAG_ZOOM = 3;
const MAG_SIZE = 130;
function magShouldShow(){ return document.getElementById('magToggle').checked; }
function updateMagActive(){ left.classList.toggle('mag-active', magShouldShow()); if(!magShouldShow()) mag.style.display='none'; }
left.addEventListener("mousemove",(e)=>{
  if(dragging||!scan.naturalWidth||!magShouldShow()){ mag.style.display='none'; return; }
  const rect=left.getBoundingClientRect();
  const mx=e.clientX-rect.left, my=e.clientY-rect.top;
  mag.style.left=(mx-MAG_SIZE/2)+'px';
  mag.style.top=(my-MAG_SIZE/2)+'px';
  const mz=zoom*MAG_ZOOM;
  mag.style.backgroundImage="url('"+scan.src+"')";
  mag.style.backgroundSize=scan.naturalWidth*mz+'px '+scan.naturalHeight*mz+'px';
  mag.style.backgroundPosition=(-(mx-imgX)*MAG_ZOOM+MAG_SIZE/2)+'px '+(-(my-imgY)*MAG_ZOOM+MAG_SIZE/2)+'px';
  mag.style.display='block';
});
left.addEventListener("mouseleave",()=>{ mag.style.display='none'; });
left.addEventListener("mousedown",(e)=>{ if(e.button!==1) return; e.preventDefault(); var cb=document.getElementById('magToggle'); cb.checked=!cb.checked; toggleMagnifier(); });

// -------------------------
// TEXT PANE
// -------------------------

function applyEditorFontSize() {
  editor.style.fontSize = editorFontSize + "px";
  document.getElementById('lineNumbers').style.fontSize = editorFontSize + "px";
  document.getElementById('highlightOverlay').style.fontSize = editorFontSize + "px";
  localStorage.setItem("proofreaderEditorFontSize", String(editorFontSize));
}

// Text pane behavior:
// Ctrl+Wheel  -> font size +/-
// Plain wheel -> scroll (default browser behaviour)
// Left drag   -> scroll text
// Simple click -> normal cursor placement
editor.addEventListener("wheel", (e) => {
  if(e.ctrlKey || e.metaKey){
    e.preventDefault();
    e.stopPropagation();
    editorFontSize += (e.deltaY < 0 ? 1 : -1);
    if (editorFontSize < 10) editorFontSize = 10;
    if (editorFontSize > 60) editorFontSize = 60;
    applyEditorFontSize();
    setStatus("font " + editorFontSize + "px");
  }
}, { passive:false });

// Disable native text drag-and-drop (browser lets you move selected text by dragging)
editor.addEventListener("dragstart", (e) => e.preventDefault());

// Latin → Greek transliteration for direct keyboard input
editor.addEventListener('keydown', (e) => {
  if(!forceGreek) return;
  if(editor.readOnly) return;
  if(e.ctrlKey||e.metaKey||e.altKey) return;
  const key=e.key;
  if(key.length===1 && /[a-zA-Z]/.test(key)){
    const g=latinToGreek[key.toLowerCase()];
    if(g){
      e.preventDefault();
      document.execCommand('insertText', false, key===key.toUpperCase() ? g.toUpperCase() : g);
    }
  }
});

editor.addEventListener("mousedown", (e) => {
  if (e.button !== 0) return;

  // In view mode (Edit off), prevent text selection so click+drag behaves like the image pane
  if (editor.readOnly) e.preventDefault();

  textDragging = true;
  textMoved = false;

  textStartX = e.clientX;
  textStartY = e.clientY;

  textScrollLeft = editor.scrollLeft;
  textScrollTop = editor.scrollTop;
});

window.addEventListener("mousemove", (e) => {
  if (!textDragging) return;

  const dx = e.clientX - textStartX;
  const dy = e.clientY - textStartY;

  if (Math.abs(dx) > 5 || Math.abs(dy) > 5) {
    textMoved = true;
    editor.scrollLeft = textScrollLeft - dx;
    editor.scrollTop = textScrollTop - dy;
  }
});

window.addEventListener("mouseup", () => {
  textDragging = false;
});

// If a drag-scroll happened, suppress the final click as much as possible.
// Simple click still works normally for cursor placement.
editor.addEventListener("click", (e) => {
  if (textMoved) {
    e.preventDefault();
    textMoved = false;
  }
}, true);


// -------------------------
// GREEK CHARACTER PALETTE
// -------------------------
const greekCharGroups = [
  {l:'α', c:'ἀ ἁ ἂ ἃ ἄ ἅ ἆ ἇ ά ὰ ᾶ ᾳ ᾴ ᾲ ᾷ ᾀ ᾁ ᾂ ᾃ ᾄ ᾅ ᾆ ᾇ'.split(' ')},
  {l:'ε', c:'ἐ ἑ ἒ ἓ ἔ ἕ έ ὲ'.split(' ')},
  {l:'η', c:'ἠ ἡ ἢ ἣ ἤ ἥ ἦ ἧ ή ὴ ῆ ῃ ῄ ῂ ῇ ᾐ ᾑ ᾒ ᾓ ᾔ ᾕ ᾖ ᾗ'.split(' ')},
  {l:'ι', c:'ἰ ἱ ἲ ἳ ἴ ἵ ἶ ἷ ί ὶ ῖ ϊ ΐ'.split(' ')},
  {l:'ο', c:'ὀ ὁ ὂ ὃ ὄ ὅ ό ὸ'.split(' ')},
  {l:'υ', c:'ὐ ὑ ὒ ὓ ὔ ὕ ὖ ὗ ύ ὺ ῦ ϋ ΰ'.split(' ')},
  {l:'ω', c:'ὠ ὡ ὢ ὣ ὤ ὥ ὦ ὧ ώ ὼ ῶ ῳ ῴ ῲ ῷ ᾠ ᾡ ᾢ ᾣ ᾤ ᾥ ᾦ ᾧ'.split(' ')},
  {l:'ρ', c:'ῤ ῥ'.split(' ')},
  {l:'σ', c:'ς \u0387 ¹ ² ³'.split(' ')},
];
function buildGreekPalette(){
  const div = document.getElementById('greekPalette');
  let html = '';
  for(const g of greekCharGroups){
    html += '<div class="row"><button class="label" onclick="insertGreekChar(\''+g.l+'\')">'+g.l+'</button><span class="chars">';
    for(const ch of g.c) html += '<button onclick="insertGreekChar(\''+ch+'\')">'+ch+'</button>';
    html += '</span></div>';
  }
  div.innerHTML = html;
}
function insertGreekChar(ch){
  editor.focus();
  document.execCommand('insertText', false, ch);
  hideGreekPalette();
}
function showGreekPalette(){ document.getElementById('greekPalette').classList.add('show'); }
function hideGreekPalette(){ document.getElementById('greekPalette').classList.remove('show'); }
function toggleGreekPalette(){
  if(editor.readOnly) return;
  const p = document.getElementById('greekPalette');
  if(p.classList.contains('show')) hideGreekPalette();
  else showGreekPalette();
}
// Close palette on click outside (except the GrChar toggle button)
document.addEventListener('click',(e)=>{
  const p = document.getElementById('greekPalette');
  if(p.classList.contains('show') && !p.contains(e.target) && e.target.id!=='grCharBtn') hideGreekPalette();
});

// Capture keyboard shortcuts before Firefox handles them.
// This prevents Ctrl+S from opening Firefox Save-As.
window.addEventListener("keydown",async(e)=>{
  const code=e.code || "";
  const isCtrl = e.ctrlKey || e.metaKey;

  // Do not intercept Ctrl+Z, Ctrl+Y, Ctrl+Shift+Z:
  // the browser textarea handles undo/redo.
  // Use event.code for app shortcuts so they work with Greek / other layouts.

  if(isCtrl && (code==="KeyS" || code==="Enter")){
    e.preventDefault();
    e.stopPropagation();
    if(e.stopImmediatePropagation) e.stopImmediatePropagation();
    await saveText();
    return false;
  }

  if(isCtrl && code==="KeyE"){
    e.preventDefault();
    document.getElementById('editToggle').click();
    return false;
  }

  if(isCtrl && code==="KeyF"){
    e.preventDefault();
    document.getElementById('findInput').focus();
    document.getElementById('findInput').select();
    return false;
  }

  if(isCtrl && code==="KeyG"){
    e.preventDefault();
    document.getElementById('forceGreekToggle').click();
    return false;
  }

  if(isCtrl && code==="KeyM"){
    e.preventDefault();
    document.getElementById('magToggle').click();
    return false;
  }

  if(isCtrl && code==="KeyD"){
    e.preventDefault();
    document.getElementById('digraphToggle').click();
    return false;
  }

  if(isCtrl && code==="KeyP"){
    e.preventDefault();
    if(!editor.readOnly) toggleGreekPalette();
    return false;
  }

  if(code==="Escape"){
    hideGreekPalette();
  }

},true);

document.getElementById('findInput').addEventListener('keydown', (e) => {
  if(e.key==='Enter') e.preventDefault();
});

function toggleForceGreek(){
  forceGreek = document.getElementById('forceGreekToggle').checked;
  localStorage.setItem('proofreaderForceGreek', forceGreek ? '1' : '0');
}
function toggleDigraph(){
  digraphMatch = document.getElementById('digraphToggle').checked;
  localStorage.setItem('proofreaderDigraph', digraphMatch ? '1' : '0');
}
function toggleLineNumbers(){
  const show = document.getElementById('showLineNums').checked;
  document.getElementById('right').classList.toggle('hide-linenums', !show);
  localStorage.setItem('proofreaderShowLineNums', show ? '1' : '0');
}
function toggleEditMode(){
  const edit = document.getElementById('editToggle').checked;
  editor.readOnly = !edit;
  editor.style.userSelect = edit ? '' : 'none';
  document.getElementById('right').classList.toggle('edit-off', !edit);
  document.getElementById('grCharBtn').disabled = !edit;
}
function toggleSettings(){
  document.getElementById('settingsModal').classList.toggle('show');
}
function toggleMagnifier(){
  const on = document.getElementById('magToggle').checked;
  updateMagActive();
  localStorage.setItem('proofreaderMagnifier', on ? '1' : '0');
}
function switchSource(idx){
  sourceIndex = parseInt(idx);
  scan.src="/image/"+encodeURIComponent(page)+"?source="+sourceIndex+"&t="+Date.now();
  connectEvents();
}
function rebuildSourceSelect(){
  const sources=pages[pageIndex] ? pages[pageIndex].sources : [];
  let html='';
  for(let i=0; i<scanDirs.length; i++){
    if(sources.indexOf(i)<0) continue;
    const sel = (i===sourceIndex) ? ' selected' : '';
    html += '<option value="'+i+'"'+sel+'>'+scanDirs[i]+'</option>';
  }
  sourceEl.innerHTML = html;
}
function rebuildTextSourceSelect(){
  let html='';
  for(let i=0; i<textDirs.length; i++){
    const sel = (i===textSourceIndex) ? ' selected' : '';
    html += '<option value="'+i+'">'+textDirs[i]+'</option>';
  }
  document.getElementById('textSourceSelect').innerHTML = html;
}
function switchTextSource(idx){
  textSourceIndex = parseInt(idx);
  if(page) loadTextForPage(page);
  connectEvents();
}
async function init(){
  const data=await fetch("/api/list").then(r=>r.json());
  scanDirs=data.scanDirs; pages=data.pages; textDirs=data.textDirs||[];
  if(pages.length===0){ setStatus("No scans found"); return; }
  // Populate page dropdown
  pageEl.innerHTML = pages.map((p,i) => '<option value="'+i+'">'+p.name+'</option>').join('');
  // Restore line-number checkbox state
  const savedShow = localStorage.getItem('proofreaderShowLineNums');
  if(savedShow === '0'){
    document.getElementById('showLineNums').checked = false;
    document.getElementById('right').classList.add('hide-linenums');
  }
  // Restore Force Greek checkbox state
  document.getElementById('forceGreekToggle').checked = forceGreek;
  // Restore Digraph matching checkbox state
  document.getElementById('digraphToggle').checked = digraphMatch;
  // Restore Magnifier checkbox state
  const savedMag = localStorage.getItem('proofreaderMagnifier');
  if(savedMag === '1'){
    document.getElementById('magToggle').checked = true;
  }
  updateMagActive();
  buildGreekPalette();
  rebuildTextSourceSelect();
  const savedKey = localStorage.getItem("proofreaderLastPage");
  const targetIdx = savedKey ? pages.findIndex(p => p.name === savedKey) : 0;
  const startIdx = targetIdx >= 0 ? targetIdx : 0;
  await loadPage(startIdx);
  // Auto-save every 30 seconds when modified
  setInterval(async () => { if(modified && page) await saveText(); }, 30000);
}
function warnUnsaved() {
  return new Promise((resolve) => {
    const modal = document.getElementById('warnModal');
    modal.classList.add('show');
    document.getElementById('warnSave').onclick = () => { modal.classList.remove('show'); resolve('save'); };
    document.getElementById('warnDiscard').onclick = () => { modal.classList.remove('show'); resolve('discard'); };
    document.getElementById('warnCancel').onclick = () => { modal.classList.remove('show'); resolve('cancel'); };
  });
}
async function maybeWarnAndProceed() {
  if (!modified) return true;
  const choice = await warnUnsaved();
  if (choice === 'save')   { await saveText(); return true; }
  if (choice === 'discard') { modified = false; return true; }
  return false; // cancel
}
async function loadTextForPage(name){
  if(!name) return;
  const data=await fetch("/api/text?page="+encodeURIComponent(name)+"&source="+textSourceIndex).then(r=>r.json());
  editor.value=data.text;
  savedText=data.text;
  updateLineNumbers();
  document.getElementById('findInput').value='';
  findMatches=[]; findIndex=0; document.getElementById('findCount').textContent='0';
  document.getElementById('highlightOverlay').innerHTML='';
  modified=false;
  updateTitle();
  setStatus(name+" ("+(pageIndex+1)+"/"+pages.length+")");
  applyEditorFontSize();
  editor.scrollTop=0;
  document.getElementById('highlightOverlay').scrollTop=0;
}
function connectEvents(){
  if(eventSource){ eventSource.close(); eventSource=null; }
  if(!page) return;
  const url="/api/events?page="+encodeURIComponent(page)+"&source="+sourceIndex+"&textSource="+textSourceIndex;
  eventSource = new EventSource(url);
  eventSource.addEventListener("image-changed", () => {
    scan.src="/image/"+encodeURIComponent(page)+"?source="+sourceIndex+"&t="+Date.now();
  });
  eventSource.addEventListener("text-changed", async () => {
    const data=await fetch("/api/text?page="+encodeURIComponent(page)+"&source="+textSourceIndex).then(r=>r.json());
    editor.value=data.text;
    savedText=data.text;
    modified=false;
    updateTitle();
    updateLineNumbers();
    document.getElementById('findInput').value='';
    findMatches=[]; findIndex=0;
    document.getElementById('findCount').textContent='0';
    document.getElementById('highlightOverlay').innerHTML='';
    setStatus(page+" (reloaded from disk)");
  });
  eventSource.onerror = () => {};
}
async function loadPage(idx){
  const entry=pages[idx];
  if(!entry) return;
  const name=entry.name;
  if(page) saveViewportState(page);
  if (!(await maybeWarnAndProceed())) { return; }
  pageIndex=idx;
  sourceIndex=0;
  textSourceIndex=0;
  page=name;
  currentTitlePage=name;
  await loadTextForPage(name);
  scan.src="/image/"+encodeURIComponent(name)+"?source="+sourceIndex+"&t="+Date.now();
  localStorage.setItem("proofreaderLastPage", name);
  document.getElementById('editToggle').checked=false;
  toggleEditMode();
  pageEl.value = String(pageIndex);
  rebuildSourceSelect();
  rebuildTextSourceSelect();
  scan.onload = () => { if(!loadViewportState(name)) fitWidth(); };
  connectEvents();
}
async function saveText(){
  if(!page) return;
  // Run save-time checks
  const warnings=checkSaveWarnings();
  if(warnings && warnings.length){
    const ok = await showSaveWarnings(warnings);
    if(!ok) return;
  }
  const res=await fetch("/api/save",{
    method:"POST",
    headers:{"Content-Type":"application/json"},
    body:JSON.stringify({page:page,text:editor.value,source:textSourceIndex})
  });
  if(!res.ok){
    setStatus("Save failed");
    return;
  }
  modified=false;
  savedText=editor.value;
  updateTitle();
  setStatus("saved "+page);
}
function showSaveWarnings(warnings){
  return new Promise((resolve) => {
    const modal = document.getElementById('saveWarnModal');
    const msg = document.getElementById('saveWarnMsg');
    msg.innerHTML = '<strong>Save warnings:</strong><br><br>'+warnings.join('<br><br>')+'<br><br>Proceed anyway?';
    modal.classList.add('show');
    document.getElementById('saveWarnContinue').onclick = () => { modal.classList.remove('show'); resolve(true); };
    document.getElementById('saveWarnCancel').onclick = () => { modal.classList.remove('show'); resolve(false); };
  });
}
async function copyAll(){
  try {
    await navigator.clipboard.writeText(editor.value);
    setStatus("copied "+editor.value.length+" chars");
  } catch(e) {
    // Fallback for older browsers
    editor.select();
    document.execCommand('copy');
    setStatus("copied (fallback)");
  }
}
async function nextPage(){ if(pageIndex<pages.length-1) await loadPage(pageIndex+1); }
async function prevPage(){ if(pageIndex>0) await loadPage(pageIndex-1); }

function zoomAt(clientX,clientY,factor){ const rect=left.getBoundingClientRect(); const mx=clientX-rect.left, my=clientY-rect.top; const oldZoom=zoom; const newZoom=clamp(zoom*factor,0.05,8.0); const ix=(mx-imgX)/oldZoom, iy=(my-imgY)/oldZoom; zoom=newZoom; imgX=mx-ix*zoom; imgY=my-iy*zoom; applyTransform(); }
function zoomInCenter(){ const r=left.getBoundingClientRect(); zoomAt(r.left+r.width/2,r.top+r.height/2,1.05); }
function zoomOutCenter(){ const r=left.getBoundingClientRect(); zoomAt(r.left+r.width/2,r.top+r.height/2,1/1.05); }
function fitWidth(){ if(!scan.naturalWidth) return; zoom=(left.clientWidth-20)/scan.naturalWidth; imgX=10; imgY=10; applyTransform(); }
function applyTransform(){
  const vpW=left.clientWidth, vpH=left.clientHeight;
  const mW=vpW*0.25, mH=vpH*0.25;
  const iW=scan.naturalWidth*zoom, iH=scan.naturalHeight*zoom;
  imgX=Math.max(vpW-iW-mW, Math.min(mW, imgX));
  imgY=Math.max(vpH-iH-mH, Math.min(mH, imgY));
  scan.style.width=iW+"px"; scan.style.height=iH+"px";
  scan.style.left=imgX+"px"; scan.style.top=imgY+"px";
}
function clamp(x,lo,hi){ return Math.max(lo,Math.min(hi,x)); }
function saveViewportState(pageName){
  if(!pageName) return;
  try { localStorage.setItem("proofreaderVp-"+pageName, JSON.stringify({zoom,imgX,imgY})); } catch(e){}
}
function loadViewportState(pageName){
  if(!pageName) return false;
  try {
    const raw = localStorage.getItem("proofreaderVp-"+pageName);
    if(!raw) return false;
    const s = JSON.parse(raw);
    zoom = s.zoom; imgX = s.imgX; imgY = s.imgY;
    applyTransform(); return true;
  } catch(e){ return false; }
}
function updateTitle(){
  const marker = modified ? "*" : "";
  const name = page ? page.replace(/\.[^.]+$/, '') : "home";
  document.title = name + " - Polytonic Proofreader" + marker;
}

function setStatus(s){ statusEl.textContent=s; }
function detectTextType(text){
  // Classify text as 'polytonic', 'neo-hellenic', or 'english'
  if(!text || !text.trim()) return null;
  let greekCount=0, polytonicCount=0, combiningCount=0, tonosCount=0, otherCombining=0;
  for(let i=0; i<text.length; i++){
    const cp=text.codePointAt(i);
    if(cp===undefined) continue;
    if(cp>=0x1F00 && cp<=0x1FFF){ polytonicCount++; greekCount++; }
    else if((cp>=0x0370 && cp<=0x03FF)||(cp>=0x1F00 && cp<=0x1FFF)||(cp>=0x0386 && cp<=0x03CE)){ greekCount++; }
    else if(cp>=0x0300 && cp<=0x036F){
      combiningCount++;
      if(cp===0x0301) tonosCount++;
      else otherCombining++;
    }
  }
  // Skip very short text
  const nonSpace=text.replace(/\s/g,'').length;
  if(nonSpace<3 && greekCount===0) return null;
  if(greekCount===0 && combiningCount===0) return 'english';
  // If any Greek Extended (precomposed polytonic) or non-tonos combining marks → polytonic
  if(polytonicCount>0 || otherCombining>0) return 'polytonic';
  // Has Greek chars but only simple tonos → neo-hellenic
  if(greekCount>0) return 'neo-hellenic';
  return 'english';
}
function checkSaveWarnings(){
  const text=editor.value;
  const warnings=[];
  // Length-drop check
  if(savedText && savedText.length>100 && text.length < savedText.length*0.5){
    const pct=Math.round(text.length/savedText.length*100);
    warnings.push('New text is only '+pct+'% of the original length ('+text.length+' vs '+savedText.length+' chars). You might be about to save an empty or truncated file.');
  }
  // Text-type check
  const type=detectTextType(text);
  if(type && textDirs[textSourceIndex]){
    const dir=textDirs[textSourceIndex];
    const typeLabel={'polytonic':'polytonic Greek','neo-hellenic':'Neo-hellenic Greek','english':'English'};
    const expected={'ocr':'polytonic','el':'neo-hellenic'};
    const expectedType=expected[dir];
    if(expectedType && type!==expectedType){
      warnings.push('This text looks like '+typeLabel[type]+', but you are saving to "'+dir+'" (expected '+typeLabel[expectedType]+').');
    }
  }
  return warnings.length ? warnings : null;
}
function getOrigSegments(cleanStart, cleanEnd, cleanToOrig){
  const segs=[];
  let ss=cleanToOrig[cleanStart], sl=1;
  for(let ci=cleanStart+1; ci<cleanEnd; ci++){
    const co=cleanToOrig[ci], po=cleanToOrig[ci-1];
    if(co===po+1){ sl++; }
    else{ segs.push({idx:ss, len:sl}); ss=co; sl=1; }
  }
  segs.push({idx:ss, len:sl});
  return segs;
}
function updateHighlights(){
  const overlay=document.getElementById('highlightOverlay');
  const term=document.getElementById('findInput').value;
  if(!term || term.replace(/\s/g,'').length<2 || !findMatches.length){
    overlay.innerHTML=''; overlay.scrollTop=editor.scrollTop; overlay.scrollLeft=editor.scrollLeft; return;
  }
  const esc=s=>s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
  const text=editor.value;
  let html='', last=0;
  for(const m of findMatches){
    html+=esc(text.slice(last, m.segments[0].idx));
    for(let si=0; si<m.segments.length; si++){
      const seg=m.segments[si];
      if(si>0) html+=esc(text.slice(m.segments[si-1].idx+m.segments[si-1].len, seg.idx));
      html+='<mark>'+esc(text.slice(seg.idx, seg.idx+seg.len))+'</mark>';
    }
    last=m.segments[m.segments.length-1].idx+m.segments[m.segments.length-1].len;
  }
  html+=esc(text.slice(last));
  overlay.innerHTML=html;
  overlay.scrollTop=editor.scrollTop;
  overlay.scrollLeft=editor.scrollLeft;
}
function doFind(){
  // Latin → Greek transliteration (user typing QWERTY without switching keyboard)
  const input=document.getElementById('findInput');
  let term;
  if(forceGreek){
    const pos=input.selectionStart; let val='', changed=false;
    for(const ch of input.value){
      const g=latinToGreek[ch.toLowerCase()];
      if(g){ val+=g; if(g!==ch) changed=true; }
      else val+=ch;
    }
    if(changed){ input.value=val; input.selectionStart=input.selectionEnd=pos; }
    term=val;
  } else {
    term=input.value;
  }
  if(!term || term.replace(/\s/g,'').length<2){
    findMatches=[]; findIndex=0; document.getElementById('findCount').textContent='0';
    updateHighlights(); return;
  }
  // 1. Remove -\n (hyphenation) → clean text + original-position map
  const text=editor.value;
  let clean=''; const c2o=[];
  for(let i=0; i<text.length; i++){
    if(text[i]==='-' && i+1<text.length && text[i+1]==='\n'){ i++; continue; }
    c2o.push(i); clean+=text[i];
  }
  // 2. NFD-normalize clean text
  const cleanNFD=clean.normalize('NFD');
  const n2c=[];
  for(let ci=0; ci<clean.length; ci++){ const nfd=clean[ci].normalize('NFD'); for(let ni=0; ni<nfd.length; ni++) n2c.push(ci); }
  // 2a. Phonetic equivalence normalization (ο↔ω, η↔ι↔υ)
  const phonMap={'ο':'ο','ω':'ο','η':'ι','ι':'ι','υ':'ι','\u00B7':'\u0387'};
  let cleanPhon='';
  for(const ch of cleanNFD){ const lc=ch.toLowerCase(); cleanPhon+=phonMap[lc]||ch; }
  // 3. Build regex (same accent-ignoring logic, plus phonetic equivalence)
  const escRx=s=>s.replace(/[.*+?^${}()|[\]\\]/g,'\\$&');
  let pat='';
  const nfdTerm=term.normalize('NFD');
  let ti=0;
  while(ti<nfdTerm.length){
    const cp=nfdTerm[ti].codePointAt(0);
    if(cp>=0x0300 && cp<=0x036F || cp===0x0345){ ti++; continue; }
    const ch1=nfdTerm[ti].toLowerCase();
    // Look ahead for a second base character (skip combining marks)
    let next=ti+1;
    while(next<nfdTerm.length){
      const ncp=nfdTerm[next].codePointAt(0);
      if(ncp>=0x0300 && ncp<=0x036F || ncp===0x0345){ next++; continue; }
      break;
    }
    let handled=false;
    if(digraphMatch && next<nfdTerm.length){
      const ch2=nfdTerm[next].toLowerCase();
      if(ch1==='ε' && ch2==='ι'){
        pat+='(?:ε[\u0300-\u036F\u0345]*ι[\u0300-\u036F\u0345]*|η[\u0300-\u036F\u0345]*|ι[\u0300-\u036F\u0345]*|υ[\u0300-\u036F\u0345]*)';
        ti=next+1; handled=true;
      }else if(ch1==='α' && ch2==='ι'){
        pat+='(?:α[\u0300-\u036F\u0345]*ι[\u0300-\u036F\u0345]*|ε[\u0300-\u036F\u0345]*)';
        ti=next+1; handled=true;
      }
    }
    if(!handled){
      const base = phonMap[ch1]||ch1;
      if(digraphMatch && base==='ι'){
        pat+='(?:ε[\u0300-\u036F\u0345]*ι[\u0300-\u036F\u0345]*|'+escRx(base)+'[\u0300-\u036F\u0345]*)';
      }else if(digraphMatch && base==='ε'){
        pat+='(?:α[\u0300-\u036F\u0345]*ι[\u0300-\u036F\u0345]*|'+escRx(base)+'[\u0300-\u036F\u0345]*)';
      }else{
        pat+=escRx(base)+'[\u0300-\u036F\u0345]*';
      }
      ti++;
    }
  }
  try{
    const rx=new RegExp(pat,'gi'); findMatches=[]; let m;
    while((m=rx.exec(cleanPhon))!==null){
      const cs=n2c[m.index], ce=n2c[m.index+m[0].length-1]+1;
      const segs=getOrigSegments(cs, ce, c2o);
      findMatches.push({idx:segs[0].idx, len:segs.reduce((a,s)=>a+s.len,0), segments:segs});
      if(m.index===rx.lastIndex) rx.lastIndex++;
    }
  }catch(e){ findMatches=[]; }
  findIndex=0;
  document.getElementById('findCount').textContent=String(findMatches.length);
  updateHighlights();
}
function jumpToFind(){
  if(!findMatches.length) return;
  editor.focus();
  const seg=findMatches[findIndex].segments[0];
  editor.setSelectionRange(seg.idx, seg.idx+seg.len);
  findIndex=(findIndex+1)%findMatches.length;
}
function updateLineNumbers(){
  const lines = editor.value.split('\n');
  const nums = document.getElementById('lineNumbers');
  let html = '';
  let lineNum = 0;
  for(let i=0; i<lines.length; i++) {
    if(lines[i].match(/^\[.*\]$/) || lines[i].trim() === '') {
      html += '<br>';
    } else {
      lineNum++;
      html += lineNum + '<br>';
    }
  }
  nums.innerHTML = html;
}

// Sync line-number + overlay scroll with textarea scroll
editor.addEventListener('scroll', () => {
  const ov = document.getElementById('highlightOverlay');
  ov.scrollTop = editor.scrollTop;
  ov.scrollLeft = editor.scrollLeft;
  document.getElementById('lineNumbers').scrollTop = editor.scrollTop;
});

window.addEventListener("beforeunload", (e) => {
  if(page) saveViewportState(page);
  if (!modified) return;
  e.preventDefault();
  e.returnValue = "";
});

init();
{{if not .RestartAvailable}}
document.getElementById('restartBtn').disabled = true;
document.getElementById('restartBtn').style.background = '#555';
document.getElementById('restartBtn').style.cursor = 'not-allowed';
document.getElementById('restartBtn').title = 'Restart unavailable (go or source file not found)';
{{end}}
</script>
</body>
</html>`

// ──── Entry point ────

func main() {
        port := "1888"
        projectDir := "."
        explicitDir := false
        for i := 1; i < len(os.Args); i++ {
                arg := os.Args[i]
                if arg == "-p" || arg == "--port" {
                        if i+1 < len(os.Args) {
                                port = os.Args[i+1]
                                i++
                        }
                } else {
                        projectDir = arg
                        explicitDir = true
                }
        }
        app, err := NewApp(projectDir)
        if err != nil { log.Fatal(err) }

        if !explicitDir {
                fmt.Printf("Using %s as book directory\n", app.ProjectDir)
        }

        goBin, err := exec.LookPath("go")
        if err == nil {
                src := findGoSource(app.ProjectDir)
                if src != "" {
                        app.SourcePath = src
                        cmd := []string{goBin, "run", src, "-p", port}
                        if projectDir != "." {
                                cmd = append(cmd, projectDir)
                        }
                        app.restartCmd = cmd
                        fmt.Printf("  Restart:   http://localhost:%s/restart\n", port)
                }
        }
        if app.restartCmd == nil {
                fmt.Printf("  Restart:   unavailable (go binary or source not found)\n")
        }

        http.HandleFunc("/", app.handleIndex)
        http.HandleFunc("/api/list", app.handleList)
        http.HandleFunc("/api/events", app.handleEvents)
        http.HandleFunc("/api/text", app.handleText)
        http.HandleFunc("/api/save", app.handleSave)
        http.HandleFunc("/api/restart", app.handleRestart)
        http.HandleFunc("/restart", app.handleRestart)
        http.HandleFunc("/image/{page}", app.handleImage)
        addr := "localhost:" + port
        fmt.Printf("\nPolytonic Proofreader running at http://%s/\n", addr)
        fmt.Printf("Project: %s\n", app.ProjectDir)
        fmt.Printf("Pages found: %d\n", len(app.Pages))
        log.Fatal(http.ListenAndServe(addr, nil))
}
