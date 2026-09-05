import argparse
import asyncio
import base64
import io
import json
import logging
import os
import re
import sys
import time
import zipfile
from pathlib import Path
from typing import Optional, Tuple

from fastapi import Depends, FastAPI, File, Form, Header, HTTPException, Query, Request, UploadFile, status
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse, PlainTextResponse, Response
from fastapi.staticfiles import StaticFiles

from app.assets import default_asset_store
from app.config import settings
from app.extractors.docx_extractor import DOCXExtractor
from app.extractors.pdf_cloud import PDFCloudExtractor
from app.extractors.pdf_local import PDFLocalExtractor
from app.extractors.pptx_extractor import PPTXExtractor
from app.extractors.xlsx_extractor import XLSXExtractor
from app.models import ExtractionResult
from app.vision import vision_client

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
)
logger = logging.getLogger("convert2text")


def verify_api_token(
    authorization: Optional[str] = Header(None),
    x_api_key: Optional[str] = Header(None, alias="X-API-Key"),
):
    """
    Validates static barrier token from .env (API_BEARER_TOKEN).
    Accepts either:
      - Header 'Authorization: Bearer <token>'
      - Header 'X-API-Key: <token>'
    If API_BEARER_TOKEN is not configured in .env, requests are permitted.
    """
    if not settings.api_bearer_token:
        return True

    provided_token = None
    if authorization:
        parts = authorization.strip().split()
        if len(parts) == 2 and parts[0].lower() == "bearer":
            provided_token = parts[1]
        else:
            provided_token = authorization.strip()
    elif x_api_key:
        provided_token = x_api_key.strip()

    if not provided_token or provided_token != settings.api_bearer_token:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Unauthorized: Invalid or missing API barrier token. Provide 'Authorization: Bearer <token>' or 'X-API-Key: <token>' header.",
            headers={"WWW-Authenticate": "Bearer"},
        )
    return True

# Initialize extractors
pdf_local_extractor = PDFLocalExtractor()
pdf_cloud_extractor = PDFCloudExtractor()
docx_extractor = DOCXExtractor()
pptx_extractor = PPTXExtractor()
xlsx_extractor = XLSXExtractor()


def detect_document_extension(file_bytes: bytes, filename: str = "") -> str:
    """
    Intelligently determines the document type from magic bytes / binary content signatures,
    with fallback to filename extension and plain text decoding.
    """
    # 1. PDF magic bytes (%PDF-)
    if file_bytes.startswith(b"%PDF-"):
        return ".pdf"

    # 2. Modern Microsoft Office XML documents (ZIP container starting with PK\x03\x04)
    if file_bytes.startswith(b"PK\x03\x04"):
        try:
            with zipfile.ZipFile(io.BytesIO(file_bytes)) as zf:
                names = zf.namelist()
                if any(n.startswith("word/") for n in names):
                    return ".docx"
                elif any(n.startswith("xl/") for n in names):
                    return ".xlsx"
                elif any(n.startswith("ppt/") for n in names):
                    return ".pptx"
        except Exception:
            pass

    # 3. Legacy Microsoft Office binary files (OLE2 compound document)
    if file_bytes.startswith(b"\xd0\xcf\x11\xe0"):
        ext = Path(filename).suffix.lower() if filename else ""
        if ext in (".doc", ".xls", ".ppt"):
            return ext
        return ".doc"

    # 4. Fallback to filename extension if provided
    if filename:
        ext = Path(filename).suffix.lower()
        if ext in (".pdf", ".docx", ".doc", ".xlsx", ".xls", ".pptx", ".ppt", ".txt", ".md", ".csv"):
            return ext

    # 5. Plain text check
    try:
        file_bytes[:1024].decode("utf-8")
        return ".txt"
    except UnicodeDecodeError:
        pass

    return Path(filename).suffix.lower() if filename else ".bin"


async def extract_document(
    file_bytes: bytes, filename: str, engine: str = "local", enable_vision: Optional[bool] = None
) -> ExtractionResult:
    """Dispatches extraction to the appropriate parser based on binary signature or extension."""
    ext = detect_document_extension(file_bytes, filename)
    logger.info("Detected document type: '%s' for filename: '%s'", ext, filename)

    if ext == ".pdf":
        if engine == "cloud":
            return await pdf_cloud_extractor.extract(file_bytes, filename)
        return await pdf_local_extractor.extract(file_bytes, filename, enable_vision=enable_vision)
    elif ext in (".docx", ".doc"):
        return await docx_extractor.extract(file_bytes, filename)
    elif ext in (".pptx", ".ppt"):
        return await pptx_extractor.extract(file_bytes, filename)
    elif ext in (".xlsx", ".xls"):
        return await xlsx_extractor.extract(file_bytes, filename)
    else:
        # Fallback to plain text decode
        try:
            text = file_bytes.decode("utf-8")
        except UnicodeDecodeError:
            text = file_bytes.decode("latin-1", errors="replace")
        return ExtractionResult(
            content=text,
            images=[],
            metadata={"filename": filename, "engine": "plain", "detected_extension": ext},
            word_count=len(text.split()),
            detected_type=ext.lstrip(".") or "txt",
        )


# Initialize FastAPI Application
app = FastAPI(
    title="Convert2Text Python",
    version="2.0.0",
    description="Production-grade RFP & Document parser to LLM-ready Markdown with Vision AI",
)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)


@app.middleware("http")
async def add_no_cache_headers(request: Request, call_next):
    response = await call_next(request)
    path = request.url.path
    if path == "/" or path.endswith((".html", ".js", ".css")):
        response.headers["Cache-Control"] = "no-cache, no-store, must-revalidate"
        response.headers["Pragma"] = "no-cache"
        response.headers["Expires"] = "0"
    return response


@app.get("/api/v1/health")
async def health_check():
    return {
        "status": "ok",
        "version": "2.0.0",
        "engines": ["local", "cloud"],
        "azure_vision_configured": vision_client.enabled if vision_client else False,
        "azure_cloud_configured": pdf_cloud_extractor.is_configured(),
        "api_barrier_protection": bool(settings.api_bearer_token),
    }


@app.get("/api/v1/assets/{filename}")
async def get_asset(filename: str):
    asset = default_asset_store.get(filename)
    if not asset:
        raise HTTPException(status_code=404, detail="Asset not found")
    return Response(content=asset["data"], media_type=asset["mime_type"])


def _clean_and_decode_base64(raw_str: str) -> bytes:
    """Sanitize and decode base64 strings with or without data URI prefix."""
    s = raw_str.strip()
    if "," in s and "base64" in s.split(",")[0]:
        s = s.split(",", 1)[1]
    s = re.sub(r"\s+", "", s)
    pad = (4 - len(s) % 4) % 4
    s += "=" * pad
    return base64.b64decode(s)


async def parse_incoming_payload(
    request: Request,
    file_upload: Optional[UploadFile] = None,
    engine_param: Optional[str] = None,
    format_param: Optional[str] = None,
    ai_vision_param: Optional[str] = None,
) -> Tuple[bytes, str, str, str, Optional[str]]:
    """
    Extracts document bytes, filename, engine, format, and ai_vision from:
    1. Standard multipart form (file: UploadFile)
    2. AI Agent JSON schema with $multipart (Azure Logic Apps / Power Automate / Dify)
    3. Direct Base64 JSON schema ({"file": "<base64>", "filename": "..."})
    4. Raw body bytes (fallback)
    """
    query_engine = request.query_params.get("engine")
    query_format = request.query_params.get("format")
    query_vision = request.query_params.get("ai_vision")

    effective_engine = engine_param or query_engine or "local"
    effective_format = format_param or query_format or "markdown"
    effective_vision = ai_vision_param or query_vision

    # 1. Direct UploadFile provided (standard multipart/form-data)
    if file_upload is not None:
        content = await file_upload.read()
        if content and len(content) > 0:
            filename = file_upload.filename or "uploaded_document"
            return content, filename, effective_engine, effective_format, effective_vision

    # 2. Inspect request body for JSON or raw data
    try:
        body_bytes = await request.body()
    except Exception:
        body_bytes = b""

    if body_bytes:
        # Try parsing JSON
        json_data = None
        try:
            json_data = json.loads(body_bytes.decode("utf-8", errors="ignore"))
        except Exception:
            pass

        if isinstance(json_data, dict):
            # 2A. AI Agent $multipart format
            # e.g.: {"$content-type": "multipart/form-data", "$multipart": [{"headers": {...}, "body": {"$content": "base64..."}}]}
            if "$multipart" in json_data and isinstance(json_data["$multipart"], list):
                extracted_content = None
                extracted_filename = "document.pdf"
                extracted_engine = effective_engine
                extracted_format = effective_format
                extracted_vision = effective_vision

                for part in json_data["$multipart"]:
                    if not isinstance(part, dict):
                        continue
                    headers = part.get("headers", {})
                    disp = headers.get("Content-Disposition") or headers.get("content-disposition", "")
                    name_m = re.search(r'name=["\']?([^"\';]+)["\']?', disp, re.IGNORECASE)
                    fname_m = re.search(r'filename=["\']?([^"\';]+)["\']?', disp, re.IGNORECASE)
                    part_name = name_m.group(1).lower() if name_m else ""
                    part_fname = fname_m.group(1) if fname_m else None

                    body = part.get("body")

                    # Extract file part
                    if part_name == "file" or part_fname or (isinstance(body, dict) and "$content" in body and fname_m):
                        if part_fname:
                            extracted_filename = part_fname
                        if isinstance(body, dict) and "$content" in body:
                            raw_val = body["$content"]
                        elif isinstance(body, str):
                            raw_val = body
                        else:
                            raw_val = str(body)

                        try:
                            extracted_content = _clean_and_decode_base64(raw_val)
                        except Exception as err:
                            logger.warning("Base64 decode failed for $multipart part: %s", err)
                            extracted_content = raw_val.encode("utf-8")
                    elif part_name == "engine":
                        val = body.get("$content", body) if isinstance(body, dict) else body
                        if val:
                            extracted_engine = str(val).strip()
                    elif part_name == "format":
                        val = body.get("$content", body) if isinstance(body, dict) else body
                        if val:
                            extracted_format = str(val).strip()
                    elif part_name == "ai_vision":
                        val = body.get("$content", body) if isinstance(body, dict) else body
                        if val:
                            extracted_vision = str(val).strip()

                if extracted_content:
                    return extracted_content, extracted_filename, extracted_engine, extracted_format, extracted_vision

            # 2B. Direct JSON with file / content field (e.g. Power Automate / Logic Apps / AI Agent)
            raw_file = (
                json_data.get("file_base64")
                or json_data.get("contentBytes")
                or json_data.get("file")
                or json_data.get("content")
                or json_data.get("file_content")
                or json_data.get("document")
                or json_data.get("data")
            )
            if raw_file:
                extracted_filename = (
                    json_data.get("file_name")
                    or json_data.get("filename")
                    or json_data.get("name")
                    or "document.pdf"
                )
                extracted_engine = (
                    str(json_data.get("engine") or "").strip()
                    or effective_engine
                    or "local"
                )
                extracted_format = json_data.get("format") or effective_format
                extracted_vision = json_data.get("ai_vision", effective_vision)

                if isinstance(raw_file, str):
                    try:
                        extracted_content = _clean_and_decode_base64(raw_file)
                    except Exception:
                        extracted_content = raw_file.encode("utf-8")
                elif isinstance(raw_file, bytes):
                    extracted_content = raw_file
                else:
                    extracted_content = str(raw_file).encode("utf-8")

                return (
                    extracted_content,
                    extracted_filename,
                    extracted_engine,
                    extracted_format,
                    str(extracted_vision) if extracted_vision is not None else None,
                )

        # 2C. Raw binary in body (application/pdf, application/octet-stream)
        if len(body_bytes) > 0 and not json_data:
            return body_bytes, "uploaded_file.pdf", effective_engine, effective_format, effective_vision

    # 3. Fallback: try request.form() directly
    try:
        form = await request.form()
        form_file = form.get("file")
        if isinstance(form_file, UploadFile):
            content = await form_file.read()
            if content:
                filename = form_file.filename or "uploaded_document"
                return (
                    content,
                    filename,
                    form.get("engine", effective_engine),
                    form.get("format", effective_format),
                    form.get("ai_vision", effective_vision),
                )
    except Exception:
        pass

    raise HTTPException(
        status_code=status.HTTP_400_BAD_REQUEST,
        detail="Missing document file. Please upload via multipart/form-data ('file') or send JSON with base64 content ('file' or '$multipart').",
    )


@app.post("/api/v1/extract")
async def api_extract(
    request: Request,
    file: Optional[UploadFile] = File(None),
    engine: Optional[str] = Form(None),
    format: Optional[str] = Form(None),
    ai_vision: Optional[str] = Form(None),
    _auth: bool = Depends(verify_api_token),
):
    start_time = time.time()
    try:
        content, filename, selected_engine, selected_format, selected_vision = await parse_incoming_payload(
            request=request,
            file_upload=file,
            engine_param=engine,
            format_param=format,
            ai_vision_param=ai_vision,
        )

        selected_engine = (selected_engine or "local").lower().strip()
        if selected_engine not in ("local", "cloud"):
            selected_engine = "local"

        is_vision_enabled = None
        if selected_vision is not None:
            if isinstance(selected_vision, bool):
                is_vision_enabled = selected_vision
            elif str(selected_vision).lower().strip() in ("true", "1", "yes"):
                is_vision_enabled = True
            elif str(selected_vision).lower().strip() in ("false", "0", "no"):
                is_vision_enabled = False

        res = await extract_document(content, filename, selected_engine, enable_vision=is_vision_enabled)
        duration_ms = int((time.time() - start_time) * 1000)

        images_data = []
        vision_count = 0
        for img in res.images:
            if img.vision_analysis:
                vision_count += 1
            images_data.append(
                {
                    "id": img.id,
                    "filename": img.filename,
                    "content_type": img.content_type,
                    "size_bytes": img.size_bytes,
                    "width": img.width,
                    "height": img.height,
                    "alt_text": img.alt_text,
                    "location": img.location,
                    "relative_path": img.relative_path,
                    "url": img.url,
                    "vision_analysis": (
                        {
                            "summary": img.vision_analysis.summary,
                            "tags": img.vision_analysis.tags,
                            "objects": img.vision_analysis.objects,
                            "extracted_text": img.vision_analysis.extracted_text,
                        }
                        if img.vision_analysis
                        else None
                    ),
                }
            )

        return {
            "success": True,
            "data": {
                "content": res.content,
                "output_format": selected_format or "markdown",
                "duration_ms": duration_ms,
                "word_count": res.word_count,
                "detected_type": res.detected_type,
                "engine": res.metadata.get("engine", selected_engine),
                "images": images_data,
                "metadata": {
                    "ai_vision_analyzed": vision_count,
                    **res.metadata,
                },
            },
        }
    except HTTPException:
        raise
    except Exception as e:
        logger.exception("Extraction failed: %s", e)
        return JSONResponse(
            status_code=500,
            content={"success": False, "error": str(e)},
        )


@app.post("/api/v1/extract/raw")
async def api_extract_raw(
    request: Request,
    file: Optional[UploadFile] = File(None),
    engine: Optional[str] = Query(None),
    _auth: bool = Depends(verify_api_token),
):
    content, filename, selected_engine, selected_format, selected_vision = await parse_incoming_payload(
        request=request,
        file_upload=file,
        engine_param=engine,
    )
    is_vision_enabled = None
    if selected_vision is not None:
        if isinstance(selected_vision, bool):
            is_vision_enabled = selected_vision
        elif str(selected_vision).lower().strip() in ("true", "1", "yes"):
            is_vision_enabled = True
        elif str(selected_vision).lower().strip() in ("false", "0", "no"):
            is_vision_enabled = False

    res = await extract_document(content, filename, selected_engine, enable_vision=is_vision_enabled)
    return PlainTextResponse(res.content, media_type="text/markdown")


# Mount static files for the Web UI
static_dir = Path(__file__).parent.parent / "static"
if not static_dir.exists():
    static_dir = Path(__file__).parent / "static"
if static_dir.exists():
    app.mount("/", StaticFiles(directory=str(static_dir), html=True), name="static")


def cli_main():
    parser = argparse.ArgumentParser(
        description="Convert2Text Python: Parse RFP & complex documents to LLM-ready Markdown"
    )
    parser.add_argument("input", nargs="?", help="Path to input document (PDF, DOCX, PPTX, XLSX)")
    parser.add_argument("output", nargs="?", help="Path to save output Markdown file")
    parser.add_argument(
        "--engine",
        choices=["local", "cloud"],
        default="local",
        help="Extraction engine: 'local' (0 cost, dynamic table detection) or 'cloud' (Azure Document Intelligence)",
    )
    parser.add_argument("--serve", action="store_true", help="Start the FastAPI web server")
    parser.add_argument("--port", type=int, default=settings.port, help="Port for web server")

    args = parser.parse_args()

    if args.serve or not args.input:
        import uvicorn

        logger.info("Starting Convert2Text server on http://localhost:%d", args.port)
        uvicorn.run("app.main:app", host="0.0.0.0", port=args.port, reload=False)
        return

    # CLI extraction mode
    input_path = Path(args.input)
    if not input_path.exists():
        print(f"Error: File not found: {args.input}", file=sys.stderr)
        sys.exit(1)

    print(f"[*] Reading file: {input_path.name}")
    file_bytes = input_path.read_bytes()

    print(f"[*] Parsing with engine: {args.engine.upper()}...")
    t0 = time.time()
    result = asyncio.run(extract_document(file_bytes, input_path.name, args.engine))
    elapsed = time.time() - t0

    print(f"[✓] Extraction finished in {elapsed:.2f}s!")
    print(f"    Engine: {result.metadata.get('engine', args.engine)}")
    print(f"    Images/Diagrams: {len(result.images)}")
    print(f"    Word count: {result.word_count:,}")

    if args.output:
        output_path = Path(args.output)
        output_path.write_text(result.content, encoding="utf-8")
        print(f"[✓] Markdown written to: {output_path.resolve()}")
    else:
        print("\n" + "=" * 50 + " MARKDOWN CONTENT " + "=" * 50 + "\n")
        print(result.content)


if __name__ == "__main__":
    cli_main()
