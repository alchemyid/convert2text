# Convert2Text 🚀

> **High-Performance Document-to-Markdown Extraction Engine (Python Dual-Engine)**  
> Ekstraksi dokumen **PDF**, **Word (.docx)**, **Excel (.xlsx, .xls)**, dan **PowerPoint (.pptx)** ke format **Markdown (.md)** atau **Plain Text (.txt)** siap pakai untuk LLM / AI Agent & Solutioning RFP.

Dilengkapi dengan **FastAPI REST API**, **CLI Dispatcher**, dan **Modern Glassmorphism Web UI**.

---

## 🌟 Fitur Utama

- ⚡ **Dual Engine Architecture**:
  1. **Local Engine (Default - Rp 0 Cost)**:
     - 100% lokal, cepat (~1.6s untuk 10 halaman PDF), privat, dan bebas biaya token/API.
     - Menggunakan `pdfplumber` untuk mendeteksi garis batas sel tabel fisik secara dinamis tanpa hardcode.
     - Menggunakan `PyMuPDF` (`pymupdf`) untuk dekode font CMap sempurna (italic, bold, symbols).
     - Menangani tabel lanjutan: multi-row header merging, pemotongan halaman berantai (*page-break continuation*), dan penggabungan sub-kolom bullet.
  2. **Cloud Precision Engine (Opsi Azure Document Intelligence)**:
     - Menggunakan Azure Document Intelligence `prebuilt-layout` model untuk dokumen hasil scan kompleks, formulir matriks, atau teks multi-kolom padat.
- 🤖 **Microsoft Azure AI Vision (Solutioning & Architecture Insights)**:
  - Analisis diagram arsitektur, bagan alur, dan OCR visual diagram teknis via **Azure AI Vision Image Analysis 4.0**.
  - **Smart Filtering**: Logo cover halaman 1 serta header/footer banner otomatis dilewati untuk menghemat kuota dan token LLM.
- 📄 **Multiformat Support**: PDF, DOCX (`python-docx`), XLSX (`openpyxl`), PPTX (`python-pptx`).
- 🔄 **2 Mode Akses**:
  1. **REST API**: Endpoint `/api/v1/extract` (multipart) dan `/api/v1/extract/raw`.
  2. **Web Interface**: Antarmuka drag-and-drop modern di `http://localhost:8080`.
  3. **CLI Terminal**: Perintah CLI langsung untuk automasi batch script.

---

## 🔬 Detail Teknis & Arsitektur Sistem

### 1. Latar Belakang & Efisiensi Biaya Token LLM
Dalam proses penelaahan dokumen tender (*RFP / KAK*), mengunggah dokumen PDF mentah (50–200 halaman) secara langsung ke LLM Multimodal memiliki kelemahan fatal:
- **Boros Token & Biaya Sangat Tinggi**: Dokumen 50 halaman dengan tabel matriks dapat mengonsumsi puluhan ribu hingga ratusan ribu token per sekali prompt.
- **Halusinasi & Kerusakan Struktur Tabel**: LLM sering kali salah membaca baris tabel yang panjang atau terpotong halaman, sehingga spesifikasi krusial (seperti kapasitas RAM, vCPU, tipe disk) bergeser kolom.
- **Font & Glyph Corruption**: Parser standar sering kali gagal membaca font subset kustom (misalnya font *italic* yang karakternya hilang atau berubah menjadi simbol aneh).

**Convert2Text** hadir sebagai jembatan deterministik berkonsumsi **Rp 0 token** yang mengonversi dokumen mentah menjadi Markdown berkepadatan informasi tinggi (*high information density*), sehingga LLM/AI Agent dapat menganalisis data teknis secara presisi dan hemat biaya.

---

### 2. Arsitektur Pemrosesan PDF Hybrid (Local Engine)
Pada mode lokal, Convert2Text menerapkan arsitektur hybrid yang menggabungkan keunggulan dua engine terkemuka:
```
                              [ File PDF Masuk ]
                                       │
                ┌──────────────────────┴──────────────────────┐
                ▼                                             ▼
     [ pdfplumber Engine ]                          [ PyMuPDF Engine ]
  • Ekstraksi batas garis fisik                  • CMap ToUnicode decoding
    (vector rules & line intersections)            (flawless italic, bold, symbol)
  • Penentuan Bounding Box tabel                 • Ekstraksi teks non-tabel
  • Rekonstruksi struktur grid sel               • Ekstraksi resolusi asli gambar
                │                                             │
                └──────────────────────┬──────────────────────┘
                                       ▼
                       [ Smart Table Normalizer ]
                       • Filter sub-tabel terduplikasi (enclosed)
                       • Auto merge multi-row headers
                       • Sambung tabel putus antar halaman
                       • Konsolidasi kolom bullet poin
                                       ▼
                       [ Natural Reading Flow Sorter ]
                       (Urutkan elemen visual dari atas ke bawah)
                                       ▼
                         [ LLM-Ready Markdown Output ]
```

---

### 3. Algoritma Cerdas Normalisasi Tabel (Zero Hardcoding)
Tools ini dirancang agar **dapat digunakan untuk dokumen apapun secara umum** tanpa mengandalkan nama kolom atau koordinat piksel yang di-hardcode:

1. **Deteksi Garis Vektor Fisik (`lines` strategy)**:
   - Menggunakan koordinat garis horizontal dan vertikal fisik untuk membangun kotak pembatas (*bounding box*) setiap sel tabel.
2. **Auto Multi-Row Header Merging**:
   - Jika dokumen memiliki header bertingkat (misalnya baris 1 berisi `"Operating"`, baris 2 berisi `"System"`), sistem otomatis mendeteksi baris data pertama (berdasarkan pola identifikasi angka/data) dan menggabungkan baris-baris header di atasnya menjadi satu baris header Markdown yang valid.
3. **Resolusi Tabel Terpotong Halaman (*Page-Break Continuation*)**:
   - Pada PDF yang dicetak dari Word, garis horizontal pembuka di puncak halaman berikutnya sering kali tidak digambar ulang.
   - Sistem secara otomatis mencari perpanjangan garis vertikal (*vertical edges*) yang menembus batas atas halaman, menutup batas sel yang terbuka secara virtual, dan mewariskan struktur header dari halaman sebelumnya.
   - Hasil: Data seperti **Server03** (halaman 4) dan **Server45** (halaman 5) masuk secara sempurna ke dalam baris tabel tanpa ada baris yang terlempar keluar.
4. **Pembersihan Kolom Pecah & Bullet Poin**:
   - Jika Word membagi sel spesifikasi menjadi kolom simbol bullet (`•`) dan kolom deskripsi teks, sistem mengenali kolom tanpa header tersebut dan menyatukannya kembali ke kolom spesifikasi dengan pemisah `<br>`.
5. **Eliminasi Sub-Tabel Bersarang (*Enclosed Tables*)**:
   - Menghapus tabel-tabel semu yang bposisinya berada 100% di dalam tabel utama untuk mencegah duplikasi konten.

---

### 4. Strategi Visual Intelligence & Filter Cerdas Azure Vision
Tidak semua gambar di dalam dokumen perlu dianalisis dengan AI Vision:
- **Filtering Halaman Cover & Banner**: Gambar pada Halaman 1 (biasanya logo perusahaan, watermark, atau background cover) otomatis **dilewati**, menghemat 100% kuota panggilan API untuk gambar yang tidak informatif.
- **Filtering Posisi & Dimensi**: Gambar dengan tinggi/lebar di bawah 150px (ikon/bullet) atau dengan aspect ratio ekstrem (> 4.0 atau < 0.25 seperti garis pemisah/banner) otomatis diabaikan.
- **Ekstraksi Semantik Diagram**: Hanya diagram arsitektur sistem, topologi jaringan, atau flowchart yang dikirim ke Azure Vision API (`Image Analysis 4.0`). Hasil analisis (teks OCR diagram, tag teknologi, dan deskripsi sistem) disematkan langsung di alur Markdown dalam bentuk blockquote.

---

### 5. Cloud Precision Engine (Azure Document Intelligence)
Untuk skenario khusus:
- Menggunakan Azure Document Intelligence model `prebuilt-layout` dengan parameter `outputContentFormat=markdown`.
- Sangat tangguh untuk dokumen hasil **scan fisik (gambar bitmap)**, tabel dengan banyak sel gabungan (*merged cells/rowspan/colspan*) yang sangat rumit, atau formulir tulisan tangan.
- **Fail-Safe Automatic Fallback**: Jika API key Azure belum dikonfigurasi atau mengalami gangguan jaringan, sistem otomatis beralih ke `PDFLocalExtractor` tanpa membuat request gagal.

---

### 6. Format Dokumen Office
- **DOCX (`python-docx`)**: Mengekstrak struktur paragraf sesuai level Heading (H1, H2, H3), memformat tabel dokumen, dan mengekstrak gambar tertanam dari relasi berkas ZIP XML.
- **PPTX (`python-pptx`)**: Mengiterasi setiap slide presentasi, memisahkan kotak teks, tabel matriks per slide, serta gambar visual.
- **XLSX (`openpyxl`)**: Membaca lembar kerja per sheet, memfilter baris kosong tak terpakai, dan menyusunnya ke dalam Markdown tabel per sheet.

---

### 7. Konfigurasi Lingkungan (`.env`)
| Variabel | Default | Deskripsi |
| :--- | :--- | :--- |
| `PORT` | `8080` | Port listening web server FastAPI & UI. |
| `MAX_UPLOAD_SIZE_MB` | `32` | Batas maksimum ukuran berkas upload. |
| `ENABLE_AI_VISION` | `true` | Aktifkan/nonaktifkan analisis visual diagram teknis. |
| `AZURE_VISION_ENDPOINT`| - | Endpoint Azure Computer Vision / AI Services. |
| `AZURE_VISION_KEY` | - | API Key Azure AI Vision. |
| `AZURE_DOC_ENDPOINT` | *(sama dgn vision)* | Endpoint Azure Document Intelligence (Layout Model). |
| `AZURE_DOC_KEY` | *(sama dgn vision)* | API Key Azure Document Intelligence. |
| `VISION_TIMEOUT_SEC` | `15` | Timeout request ke Azure Vision. |

---

## 🏗️ Struktur Proyek

```
convert2text/
├── app/
│   ├── __init__.py
│   ├── config.py             # Pengaturan environment (.env)
│   ├── models.py             # Data models (ExtractionResult, ExtractedImage, VisionAnalysis)
│   ├── assets.py             # Storage memory & hash untuk aset gambar
│   ├── vision.py             # Azure AI Vision client & filter
│   ├── main.py               # FastAPI application & CLI dispatcher
│   └── extractors/
│       ├── base.py           # BaseExtractor & table/image markdown formatters
│       ├── pdf_local.py      # Local PDF parser (pdfplumber + pymupdf)
│       ├── pdf_cloud.py      # Cloud PDF parser (Azure Document Intelligence)
│       ├── docx_extractor.py # Word (.docx) parser
│       ├── pptx_extractor.py # PowerPoint (.pptx) parser
│       └── xlsx_extractor.py # Excel (.xlsx) parser
├── static/                   # Frontend Web UI (HTML, CSS, JS)
├── ARCHIVE/                  # Arsip kode legacy Golang (cmd/, internal/, go.mod, go.sum)
├── .env                      # Kredensial & konfigurasi lokal
├── .env.example              # Template konfigurasi environment
├── requirements.txt          # Dependensi Python
├── Dockerfile                # Dockerfile container Python 3.12-slim
└── hasil.md                  # Contoh hasil ekstraksi Markdown
```

---

## 🚀 Panduan Memulai

### 1. Setup Virtual Environment
```bash
# Buat virtual environment
/usr/local/bin/virtualenv -p /usr/local/bin/python3 .venv

# Install dependensi
.venv/bin/pip install -r requirements.txt
```

### 2. Jalankan Ekstraksi via CLI
```bash
# Mode Local (Rp 0 Cost - Default)
.venv/bin/python -m app.main "document.pdf" output.md --engine=local

# Mode Cloud Precision (Azure Document Intelligence)
.venv/bin/python -m app.main "document.pdf" output.md --engine=cloud
```

### 3. Jalankan Web UI & REST Server Secara Lokal
```bash
.venv/bin/python -m app.main --serve --port 8080
# atau
.venv/bin/uvicorn app.main:app --host 0.0.0.0 --port 8080
```
Buka browser di: **http://localhost:8080**

### 4. Deploy Production dengan Docker Compose (Nginx + Python Backend)
Aplikasi telah dilengkapi konfigurasi **Docker Compose** dan reverse proxy **Nginx** untuk menangani domain kustom, SSL, buffer berkas besar (hingga 64 MB), dan timeout ekstraksi panjang:

```bash
# Build dan jalankan seluruh container (app & nginx)
docker compose up -d --build

# Periksa status container
docker compose ps

# Melihat log aplikasi
docker compose logs -f app
```

- **Akses Domain / Host**: Nginx me-listen di port `80` (dan `443`). Buka browser di `http://your-domain.com` atau `http://localhost`.
- **Konfigurasi Domain**: Edit `nginx/conf.d/default.conf` dan ubah `server_name` sesuai domain Anda.
- **Konfigurasi SSL/HTTPS**:
  1. Letakkan sertifikat SSL (`fullchain.pem` dan `privkey.pem`) di dalam folder `nginx/ssl/`.
  2. Gunakan template `nginx/conf.d/default-ssl.conf.example` sebagai panduan konfigurasi HTTPS siap pakai.

---

## 📡 REST API Reference

Dokumentasi lengkap format request dan response untuk integrasi API:

---

### 1. `POST /api/v1/extract`
Endpoint utama untuk mengunggah dokumen dan menerima respons JSON terstruktur lengkap dengan konten Markdown, statistik ekstraksi, daftar aset gambar, dan analisis AI Vision.

#### Request
- **Method**: `POST`
- **Content-Type**: `multipart/form-data`
- **Parameters (Form Data)**:
  - `file` *(wajib)*: Berkas dokumen biner (`.pdf`, `.docx`, `.xlsx`, `.pptx`).
  - `engine` *(opsional)*: Pilihan engine:
    - `"local"` *(default)*: Parsing 100% lokal, cepat (~1.6s), Rp 0 token/API cost.
    - `"cloud"`: Azure Document Intelligence Layout Model.
  - `format` *(opsional)*: Format keluaran (`"markdown"` default, atau `"text"`).
  - `ai_vision` *(opsional)*: `"true"` atau `"false"` untuk mengaktifkan AI Vision pada diagram teknis.

#### Contoh Request (cURL)
```bash
curl -X POST "http://localhost:8080/api/v1/extract" \
  -F "file=@2025 KAK Pengadaan Solusi AWS DRS_SF.pdf" \
  -F "engine=local" \
  -F "format=markdown" \
  -F "ai_vision=true"
```

#### Response Sukses (`200 OK`)
```json
{
  "success": true,
  "data": {
    "content": "# Kerangka Acuan Kerja\n\n| No | Server | Operating System | ...\n|---|---|---|...\n| 1 | Server01 | Windows | ...",
    "output_format": "markdown",
    "duration_ms": 1644,
    "word_count": 3345,
    "detected_type": "pdf",
    "engine": "local",
    "images": [
      {
        "id": "e3b0c44298fc1c149afbf4c8",
        "filename": "document.pdf_p2_img_1.jpeg",
        "content_type": "image/jpeg",
        "size_bytes": 48210,
        "width": 850,
        "height": 520,
        "alt_text": "Diagram / Figure on Page 2",
        "location": "Page 2",
        "relative_path": "./assets/document.pdf_p2_img_1.jpeg",
        "url": "/api/v1/assets/e3b0c44298fc1c149afbf4c8.jpeg",
        "vision_analysis": {
          "summary": "Diagram arsitektur solusi replikasi AWS DRS dari On-Premise ke VPC AWS Cloud.",
          "tags": ["cloud computing", "software architecture", "amazon web services", "diagram"],
          "objects": ["server", "database", "cloud"],
          "extracted_text": ["AWS Elastic Disaster Recovery", "On-Premises Data Center", "AWS Region Jakarta", "VPC", "Replication Server"]
        }
      }
    ],
    "metadata": {
      "filename": "2025 KAK Pengadaan Solusi AWS DRS_SF.pdf",
      "total_pages": 10,
      "engine": "local",
      "ai_vision_analyzed": 1
    }
  }
}
```

#### Penjelasan Field Response
| Field | Tipe | Deskripsi |
| :--- | :--- | :--- |
| `success` | `boolean` | Status keberhasilan (`true` jika berhasil, `false` jika gagal). |
| `data.content` | `string` | Teks hasil ekstraksi dalam format Markdown atau Plain Text siap konsumsi LLM. |
| `data.output_format` | `string` | Format output yang dihasilkan (`"markdown"` atau `"text"`). |
| `data.duration_ms` | `integer` | Waktu pemrosesan dokumen di server (dalam milidetik). |
| `data.word_count` | `integer` | Jumlah kata yang berhasil diekstrak. |
| `data.detected_type` | `string` | Tipe file yang terdeteksi (`"pdf"`, `"docx"`, `"xlsx"`, `"pptx"`). |
| `data.engine` | `string` | Engine yang memproses dokumen (`"local"` atau `"cloud"`). |
| `data.images` | `array` | Daftar objek gambar/diagram teknis yang diekstrak dari dokumen. |
| `images[].url` | `string` | Endpoint URL lokal untuk mengakses file gambar (`/api/v1/assets/...`). |
| `images[].vision_analysis` | `object` | Hasil analisis Azure Vision AI (bila gambar berupa diagram arsitektur). |
| `vision_analysis.summary` | `string` | Penjelasan ringkas isi diagram teknis. |
| `vision_analysis.tags` | `array` | Entitas & teknologi yang teridentifikasi di diagram. |
| `vision_analysis.extracted_text`| `array` | Teks OCR yang tertulis di dalam diagram arsitektur. |
| `data.metadata` | `object` | Informasi halaman, nama file, dan jumlah gambar yang dianalisis. |

#### Response Error (`400 / 500`)
```json
{
  "success": false,
  "error": "Uploaded file is empty or unsupported format"
}
```

---

### 2. `POST /api/v1/extract/raw`
Endpoint ringan yang langsung mengembalikan konten teks mentah (`text/markdown` atau `text/plain`) tanpa pembungkus JSON. Sangat cocok untuk CLI pipe (`|`) atau automasi shell script.

#### Request
- **Method**: `POST`
- **Query Parameter**: `engine=local` *(default)* atau `engine=cloud`
- **Content-Type**: `multipart/form-data` (file pada key `file`)

#### Contoh Request (cURL)
```bash
curl -X POST "http://localhost:8080/api/v1/extract/raw?engine=local" \
  -F "file=@2025 KAK Pengadaan Solusi AWS DRS_SF.pdf" \
  -o output.md
```

---

### 3. `GET /api/v1/assets/{filename}`
Mengunduh atau menampilkan gambar yang diekstrak dari dokumen.

#### Request
- **Method**: `GET`
- **Response**: Binary data gambar (`image/png`, `image/jpeg`, dsb.).

---

### 4. `GET /api/v1/health`
Mengecek status kesehatan server dan ketersediaan engine.

#### Response (`200 OK`)
```json
{
  "status": "ok",
  "version": "2.0.0",
  "engines": ["local", "cloud"],
  "azure_vision_configured": true,
  "azure_cloud_configured": true
}
```

