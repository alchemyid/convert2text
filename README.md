# Convert2Text 🚀

> **High-Performance Document-to-Markdown/Text Extraction Engine in Go**  
> Ekstraksi dokumen **PDF**, **Word (.docx)**, **Excel (.xlsx, .xls)**, dan **PowerPoint (.pptx)** ke format **Markdown (.md)** atau **Plain Text (.txt)**.

Dilengkapi dengan **REST API** (mendukung binary & multipart upload) dan **Modern Web UI** yang di-embed langsung ke dalam single binary.

---

## 🌟 Fitur Utama

- 📄 **Multiformat Support**: PDF, DOCX, XLSX, dan PPTX.
- 🖼️ **AI Agent Image Extraction & Semantic Placeholders**:
  - Mengekstrak gambar/diagram dari DOCX, PPTX, PDF, dan Excel ke asset store (`/api/v1/assets/{id}`).
  - Menyematkan referensi semantik terstruktur (`[IMAGE: name | Alt: descr]`) langsung di alur Markdown/Text.
  - **Menghemat 80%–95% Vision Tokens** saat dokumen RFP/tender dikirim ke LLM / AI Agent.
- 📊 **Smart Table & Layout Detection**:
  - Deteksi tabel otomatis dan rendering ke Markdown table rapi.
  - Dynamic CMap ToUnicode resolver memperbaiki decoding font subset kustom.
- 🔄 **2 Mode Akses**:
  1. **REST API**: Integrasi langsung dengan backend aplikasi atau automasi Linux pipe.
  2. **Web Interface**: Antarmuka drag-and-drop modern (glassmorphism dark mode) dengan live rendered markdown preview, raw editor, image gallery viewer, instant copy, dan download.
- ⚡ **Compute & Resource Optimization**:
  - **Worker Semaphore Limiter**: Membatasi penggunaan komputasi/CPU agar server tidak overload saat request tinggi.
  - **Disk Spooling**: Payload stream disimpan sementara ke file disk temporer terisolasi, menjaga penggunaan RAM tetap rendah.
  - **Excel Row Streaming**: Membaca baris Excel secara on-demand via row iterator tanpa memuat seluruh lembar kerja ke RAM.
- 🛡️ **Security-First Architecture**:
  - **Magic Bytes & Archive Structure Check**: Menolak file palsu / ekstensi spoofed.
  - **Zip Bomb / Decompression Bomb Protection**: Enforce limit uncompressed bytes (`CountingLimitReader`) untuk DOCX, PPTX, dan XLSX.
  - **Strict Request Size Boundary**: Batas upload (`http.MaxBytesReader`) mencegah DoS serangan ukuran besar.
  - **Path Traversal Defense**: Sanitasi nama file ketat (`filepath.Base` dan regex replacement).
  - **Security Headers & CORS**: `X-Content-Type-Options`, `X-Frame-Options`, `Content-Security-Policy`.
- 🧩 **DRY (Don't Repeat Yourself)**:
  - Unified `Extractor` interface & parser factory.
  - Reusable streaming readers, table markdown formatters, text cleaners, dan response envelopes.

---

## 🏗️ Struktur Proyek

```
convert2text/
├── cmd/
│   └── server/
│       └── main.go               # Server entrypoint & graceful shutdown
├── internal/
│   ├── assets/
│   │   └── store.go              # Image asset cache & storage with eviction
│   ├── config/
│   │   └── config.go             # Environment configuration (Port, limits, concurrency)
│   ├── extractor/
│   │   ├── extractor.go          # Interface, Magic Bytes detector, CountingLimitReader, DRY helpers
│   │   ├── docx.go               # Word (.docx) XML & media extractor
│   │   ├── pptx.go               # PowerPoint (.pptx) XML & media extractor
│   │   ├── excel.go              # Excel (.xlsx) row streaming & media extractor
│   │   ├── pdf.go                # PDF layout, table & image extractor
│   │   └── extractor_test.go     # Unit & integration tests
│   ├── middleware/
│   │   ├── security.go           # Security headers, CORS, MaxBytesReader
│   │   └── limiter.go            # Concurrency semaphore worker limiter
│   ├── handler/
│   │   ├── api.go                # REST API extraction & asset endpoints
│   │   ├── health.go             # Compute, Goroutine, & Memory metrics endpoint
│   │   ├── response.go           # Standard JSON response helpers
│   │   └── api_test.go           # Handler tests
│   └── web/
│       ├── embed.go              # Single binary embed.FS
│       └── static/
│           ├── index.html        # Web UI with Image Gallery
│           ├── style.css         # Modern glassmorphism styles
│           └── app.js            # Frontend logic
├── Dockerfile                    # Multi-stage production container
├── go.mod
└── go.sum
```

---

## 🚀 Menjalankan Aplikasi

### 1. Jalankan Langsung dengan Go
```bash
go run ./cmd/server/main.go
```
Buka browser Anda di `http://localhost:8080` untuk mengakses Web Interface.

### 2. Build Binary Mandiri (Single Binary)
```bash
go build -ldflags="-s -w" -o convert2text ./cmd/server/main.go
./convert2text
```

### 3. Menggunakan Docker
```bash
docker build -t convert2text .
docker run -p 8080:8080 convert2text
```

---

## 📡 REST API Reference

### 1. `POST /api/v1/extract` (Respon JSON)
Mengekstrak file dan mengembalikan dokumen teks/markdown bersama metadata dan statistik.

#### Request (Multipart Form Data):
```bash
curl -X POST "http://localhost:8080/api/v1/extract" \
  -F "file=@document.docx" \
  -F "format=markdown"
```

#### Request (Direct Binary Payload):
```bash
curl -X POST "http://localhost:8080/api/v1/extract?format=markdown&filename=report.xlsx" \
  --data-binary @report.xlsx
```

#### Contoh Response:
```json
{
  "success": true,
  "data": {
    "filename": "document.docx",
    "detected_type": "docx",
    "output_format": "markdown",
    "content": "# Project Title\n\nIsi dokumen yang diekstrak...\n",
    "word_count": 420,
    "duration_ms": 14,
    "metadata": {
      "file_format": "docx"
    }
  }
}
```

---

### 2. `POST /api/v1/extract/raw` (Direct Stream Raw)
Mengekstrak file dan langsung menghasilkan isi plain text / markdown dengan header `Content-Type: text/markdown` atau `text/plain`.

```bash
# Simpan hasil ekstraksi langsung ke file
curl -X POST "http://localhost:8080/api/v1/extract/raw" \
  -F "file=@financials.xlsx" \
  -F "format=markdown" > financials.md
```

---

### 3. `POST /api/v1/extract/bundle` (Download ZIP Asset Bundle)
Mengekstrak file dan langsung mengembalikan **file `.zip`** (`Content-Type: application/zip`) yang berisi:
- Dokumen Markdown (`document.md`) dengan path gambar relatif: `![Alt](./assets/image.png)`.
- Folder `assets/` berisi seluruh gambar/diagram biner yang diekstrak.

**Tanpa perlu Base64!** Aplikasi client atau script Linux Anda dapat langsung mengunduh dan mengekstraknya secara instan:

```bash
curl -X POST "http://localhost:8080/api/v1/extract/bundle" \
  -F "file=@devops_tools.pdf" \
  -F "format=markdown" -o devops_tools.zip

# Unzip dan buka di VS Code / Obsidian / Viewer offline
unzip devops_tools.zip -d devops_tools/
```

---

### 4. `GET /api/v1/assets/{id}` (On-Demand Image Fetch)
Mengambil file gambar biner yang diekstrak berdasarkan URL atau ID referensi yang ada pada tag placeholder markdown (`[IMAGE: ...]`).
Sangat ideal untuk pola **Lazy Loading AI Agent**: AI Agent hanya memanggil endpoint ini ketika benar-benar perlu menginspeksi diagram tertentu secara visual!

```bash
curl http://localhost:8080/api/v1/assets/a1b2c3d4e5f6.png --output diagram.png
```

---

### 4. `GET /api/v1/health` (Monitoring Compute & Memory)
Menampilkan utilisasi komputasi, CPU goroutines, memori, dan worker semaphore aktif.

```bash
curl http://localhost:8080/api/v1/health
```

#### Contoh Response:
```json
{
  "success": true,
  "data": {
    "status": "healthy",
    "uptime": "12m34s",
    "compute": {
      "num_cpu": 8,
      "num_goroutines": 12,
      "active_workers": 0,
      "max_workers": 16
    },
    "memory": {
      "alloc_mb": 14,
      "total_alloc_mb": 42,
      "sys_mb": 32,
      "num_gc": 3
    }
  }
}
```

---

## ⚙️ Konfigurasi Environment Variables

| Variable | Default | Deskripsi |
|---|---|---|
| `PORT` | `8080` | Port HTTP server |
| `MAX_UPLOAD_SIZE_MB` | `32` | Batas maksimum ukuran file yang diupload (MB) |
| `MAX_CONCURRENT_EXTRACTIONS` | `CPU*2` | Batas serentak worker komputasi ekstraksi |
| `MAX_DECOMPRESSED_SIZE_MB` | `150` | Batas proteksi Decompression Bomb (Zip Bomb) |
| `EXTRACTION_TIMEOUT_SEC` | `60` | Batas timeout per request ekstraksi (detik) |

---

## 🧪 Menjalankan Pengujian (Tests)

```bash
go test -v ./...
```
Semua test menguji:
- Ekstraksi format DOCX, PPTX, XLSX
- Proteksi Zip Bomb / dekompresi ekstrem
- Sanitasi nama file & path traversal prevention
- Endpoint REST API (Multipart, Binary Stream, Error handling)
