# Polytonic Proofreader

A browser-based polytonic Greek OCR proofreader.

> Originally created for proofreading **ΤΟ ΣΥΜΠΑΝ (THE UNIVERSE)** (Smyrna, 1888) —
> a family heirloom the author wanted to digitise and read.
> The TCP port `1888` is the year of printing; the name was generalised
> because polytonic Greek OCR is a broader need. You review scanned pages side-by-side with their OCR output, correct the text, and save — all from a browser tab next to your AI/OCR tool.

## Workflow

1. **Place page images** — put your scanned page images (JPG, PNG, WebP, GIF, TIF) into the `scans/` folder inside your project directory.
2. **Get OCR text from an AI vision model** — in another browser tab, feed each scan image to an AI vision model (e.g. Gemini, GPT‑4o, Claude) together with a prompt instructing it to output polytonic Greek. Copy the returned OCR text.
3. **Navigate to the page** — use the **← Previous** / **Next →** toolbar buttons to go to the page you just processed.
4. **Paste the OCR** — paste the AI's output into the text pane on the right.
5. **Proofread** — compare the scan image (left) against the text (right). Use the **accent‑ignoring search** (Ctrl+F) to spot-check dubious words, and the **Greek character palette** (Ctrl+P) to type corrections. The AI vision model can also suggest what to check again — follow its recommendations.
6. **Read and annotate** — the purpose is not just to correct OCR errors, but to actually **read the book** as you go. Add comments for small errors in the original text, noteworthy topics, or anything that catches your interest — inside brackets like `[λέξη: επεξήγηση]` or as plain notes. These annotations persist in the saved text.
7. **Save and move on** — press **Save** (Ctrl+S) and repeat for the next page. Unsaved changes show a `*` in the browser tab title.

The goal is to catch what the machine got wrong, not to type every page from scratch — and to read and engage with the text along the way.

> See [`gemini-prompt.txt`](gemini-prompt.txt) for the prompt used with Gemini to obtain polytonic Greek OCR output.

## How it works

```
project/
├── scans/       # page images (jpg, png, webp, gif, tif)
│    ├── 011.jpg
│    ├── 011b.jpg      ← multiple variants grouped by 3‑letter prefix
│    └── 012.jpg
├── ocr/         # OCR text files (auto‑created, one per page)
│    ├── 011.txt
│    └── 012.txt
└── ...
```

Pages sharing the same 3‑character prefix (e.g. `011.jpg`, `011b.jpg`) are grouped together. Use the **source dropdown** in the toolbar to switch between image variants while keeping the same text.

## Quick start

```bash
go run proofreader.go /path/to/project
# Open http://localhost:1888/
```

## Features

### Side-by-side view
- **Left pane** — scan image (wheel to scroll vertically, **Ctrl+wheel** to zoom, click-drag to pan, double-click to fit/zoom)
- **Right pane** — editable OCR text with wheel to scroll, **Ctrl+wheel** to change font size

### Edit mode
Click **☐ Edit** (or **Ctrl+E**) to toggle between:

| Mode | Behaviour |
|---|---|
| View | Drag‑scroll text, font resize via scrollwheel, read‑only |
| Edit | Type corrections, click for cursor, undo/redo work |

### Polytonic Greek character palette
Click **Greek Char** (or **Ctrl+P**) to open a popup with all accented/breathing‑mark variants. Click any character to insert it at the cursor position. The leftmost column (the bare letter) is also clickable — you can insert `α` without switching your keyboard.

**Undo (Ctrl+Z) works** for palette insertions and direct typing.

### Latin → Greek transliteration ("Force Greek")
When **☐ Force Greek** is checked (default), both the **search box** and the **text area** automatically convert Latin letters to their Greek QWERTY equivalents as you type:

| English key | → | Greek |
|---|---|---|
| a | → | α |
| b | → | β |
| g | → | γ |
| d | → | δ |
| e | → | ε |
| z | → | ζ |
| h | → | η |
| u | → | θ |
| i | → | ι |
| k | → | κ |
| l | → | λ |
| m | → | μ |
| n | → | ν |
| x | → | χ |
| o | → | ο |
| p | → | π |
| r | → | ρ |
| s | → | σ |
| t | → | τ |
| y | → | υ |
| f | → | φ |
| c | → | ψ |
| v | → | ω |
| q | → | ς |
| j | → | ξ |

Uncheck to type Latin letters normally. The setting persists across page changes and browser refreshes via `localStorage`.

### Search
The search field finds polytonic Greek text, **ignoring accents and breathing marks**. Typing `καλος` also matches `καλός`, `καλῶς`, etc. Search is hyphenation‑aware: line‑break hyphens (`-\n`) are skipped during matching.

### Copy All
Click **Copy All** to copy the entire current page text to your clipboard. Shows a confirmation in the status bar.

### Digraph matching (experimental)
When **☐ Digraph matching** is checked, the search engine also matches historical spelling variants:
- ει matches η, ι, υ (all pronounced similarly in later Greek)
- αι matches ε
This helps find words that may have been spelled with different vowel letters in the original text vs. the OCR output.

### Line numbers
Click **☐ Lines** to toggle line numbers on the left side of the text pane. Lines that are blank or contain only metadata (e.g. `[header]`) are skipped by the numbering.

### Image source selector
Use the **source dropdown** (between the page selector and the text source) to switch between multiple scan directories — e.g. `scans` (primary) and `scans2` (alternative/cleaner images of the same pages).

### Text source selector
Use the **text source dropdown** (next to the image source) to switch between multiple OCR sources — e.g. `ocr` (polytonic Greek) and `el` (modern Greek). This allows comparing or saving to different text directories.

### Server restart (hot‑reload)
After editing the Go source, click the **Restart** button (or visit `http://localhost:1888/restart`) to recompile and restart the server in-place. The browser automatically refreshes after 1 second — no need to restart the terminal command.

## Keyboard shortcuts

| Shortcut | Action |
|---|---|
| **Ctrl+E** | Toggle edit mode |
| **Ctrl+F** | Focus search box |
| **Ctrl+G** | Toggle Force Greek |
| **Ctrl+D** | Toggle digraph matching |
| **Ctrl+P** | Open Greek character palette |
| **Ctrl+S** | Save current page |
| **Ctrl+Z / Y / Shift+Z** | Undo / redo (native browser, always works) |
| **Escape** | Close Greek palette |

## Project structure

```
├── proofreader.go       # single‑file Go server + embedded HTML/JS/CSS
├── README.md
├── .gitignore
```

No dependencies beyond the Go standard library. No `go.mod`, no build step — just `go run`.

## Development and AI-assisted modification

The entire application is a **single Go file** (`proofreader.go`) with the HTML, CSS, and JavaScript embedded in a Go string constant. This makes it an ideal target for AI-assisted modification:

- **Describe the feature** you want in natural language to an AI code agent, paste the file, and let it propose the changes.
- **Add a new keyboard shortcut** — one `if(isCtrl && code==="KeyX")` block in the `keydown` listener.
- **Add a new toolbar button** — one HTML line in the toolbar section, one JS function.
- **Modify the Greek character palette** — edit the `greekCharGroups` array.
- **Extend the transliteration map** — add entries to `latinToGreek`.
- **Port to another language/framework** — the logic is self-contained: a file watcher, an HTTP server, and a browser GUI. An AI agent can translate each layer while keeping the behaviour.

The `localStorage` keys all start with `proofreader` — if you fork the tool, give it a custom prefix to avoid conflicts with other forks (search for `proofreader` in the JS and replace).

## Forking for another language

This tool was built for polytonic Greek, but the **Go server is completely language-agnostic** — only the embedded frontend contains script-specific code. To fork it for any other writing system (Hebrew, Arabic, Cyrillic, Devanagari, etc.):

### What stays the same (no changes needed)

All Go server code, file discovery, image display, zoom/pan, save/load, page navigation, hot-restart, and line numbering — none of it knows or cares what script you're using.

### What to replace

| Component | Where in `proofreader.go` | What to do |
|---|---|---|
| **Character palette** | `greekCharGroups` array in the JS (~line 537) | Replace each group of characters with your script's letters and their diacritic variants. The leftmost button in each row is the base letter; the rest are variants. |
| **Transliteration map** | `latinToGreek` object in the JS (~line 411) | Map Latin QWERTY keys to your script's base letters so users can type without switching keyboards. Set `forceGreek` to `false` if your script doesn't benefit from this. |
| **Text detection** | `detectTextType()` function in the JS (~line 841) | Update the Unicode range checks to detect your script vs. Latin vs. other text. |
| **Search** | `doFind()` function in the JS (~line 921) | Keep the NFD normalisation (strips combining marks) for any script with diacritics. Replace the phonetic-equivalence map (ο↔ω, η↔ι↔υ) with your own. |
| **Save warnings** | `checkSaveWarnings()` in the JS (~line 866) | Update the text-type labels and expected directories for your script. |
| **`localStorage` prefix** | All `proofreader*` keys in the JS | Replace `proofreader` with your own prefix (e.g. `hebrew-proofreader`). |

### Step-by-step

1. **Copy** `proofreader.go` → `your-fork.go`
2. **Edit** the `indexHTML` constant (a Go string containing the whole frontend). Change the colour scheme, fonts, character palette, and transliteration map to suit your script.
3. **Search and replace** the `localStorage` prefix: every `proofreader` in the JS → your custom prefix.
4. **Update** `findGoSource()` in the Go server section (just before the embedded frontend) to look for `your-fork.go`.
5. **Update** the HTML `<title>` and `document.title` to your project name.
6. **Run** with `go run your-fork.go /path/to/project`.

### Important: keep the contract

The Go server calls specific JS function names and expects specific HTML element IDs (`editor`, `pageSelect`, `sourceSelect`, `findInput`, etc.). When rewriting the frontend, **preserve the IDs and function signatures** listed below — otherwise the server ↔ frontend communication will break:

- Element IDs: `editor`, `pageSelect`, `sourceSelect`, `textSourceSelect`, `scan`, `findInput`, `findCount`, `status`, `lineNumbers`, `highlightOverlay`
- JS functions called from Go template or inline handlers: `prevPage()`, `nextPage()`, `loadPage(idx)`, `saveText()`, `switchSource(val)`, `switchTextSource(val)`, `doFind()`, `toggleGreekPalette()`, `toggleEditMode()`
- API endpoints (unchanged): `/api/list`, `/api/text`, `/api/save`, `/api/restart`, `/image/{page}`

Everything else — layout, styling, keyboard shortcuts, palette position — is yours to redesign.

---

> Developed with the assistance of **Reasonix Code (Deepseek V4)**, an AI coding agent.
> The design, workflow, and feature decisions were made by the author;
> the agent translated them into code.

## License

MIT. Do what you want with it.
