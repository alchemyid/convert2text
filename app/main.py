import argparse
import asyncio
import logging
import os
import sys
import time
from pathlib import Path
from typing import Optional

from fastapi import FastAPI, File, Form, HTTPException, Query, UploadFile
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

# Initialize extractors
pdf_local_extractor = PDFLocalExtractor()
pdf_cloud_extractor = PDFCloudExtractor()
docx_extractor = DOCXExtractor()
pptx_extractor = PPTXExtractor()
xlsx_extractor = XLSXExtractor()


async def extract_document(
    file_bytes: bytes, filename: str, engine: str = "local"
) -> ExtractionResult:
    """Dispatches extraction to the appropriate parser based on file extension and selected engine."""
    ext = Path(filename).suffix.lower()

    if ext == ".pdf":
        if engine == "cloud":
            return await pdf_cloud_extractor.extract(file_bytes, filename)
        return await pdf_local_extractor.extract(file_bytes, filename)
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
            metadata={"filename": filename, "engine": "plain"},
            word_count=len(text.split()),
            detected_type="txt",
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


@app.get("/api/v1/health")
async def health_check():
    return {
        "status": "ok",
        "version": "2.0.0",
        "engines": ["local", "cloud"],
        "azure_vision_configured": vision_client.enabled if vision_client else False,
        "azure_cloud_configured": pdf_cloud_extractor.is_configured(),
    }


@app.get("/api/v1/assets/{filename}")
async def get_asset(filename: str):
    asset = default_asset_store.get(filename)
    if not asset:
        raise HTTPException(status_code=404, detail="Asset not found")
    return Response(content=asset["data"], media_type=asset["mime_type"])


@app.post("/api/v1/extract")
async def api_extract(
    file: UploadFile = File(...),
    engine: str = Form("local"),
    format: Optional[str] = Form("markdown"),
    ai_vision: Optional[str] = Form(None),
):
    start_time = time.time()
    try:
        content = await file.read()
        if not content:
            raise HTTPException(status_code=400, detail="Uploaded file is empty")

        selected_engine = (engine or "local").lower().strip()
        if selected_engine not in ("local", "cloud"):
            selected_engine = "local"

        res = await extract_document(content, file.filename or "uploaded_file", selected_engine)
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
                "output_format": format or "markdown",
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
    except Exception as e:
        logger.exception("Extraction failed: %s", e)
        return JSONResponse(
            status_code=500,
            content={"success": False, "error": str(e)},
        )


@app.post("/api/v1/extract/raw")
async def api_extract_raw(
    file: UploadFile = File(...),
    engine: str = Query("local"),
):
    content = await file.read()
    if not content:
        raise HTTPException(status_code=400, detail="Uploaded file is empty")

    res = await extract_document(content, file.filename or "uploaded_file", engine)
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
