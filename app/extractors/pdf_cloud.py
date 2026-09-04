import asyncio
import logging
from typing import Optional
import httpx

from app.models import ExtractionResult
from app.extractors.base import BaseExtractor
from app.extractors.pdf_local import PDFLocalExtractor
from app.config import settings

logger = logging.getLogger(__name__)


class PDFCloudExtractor(BaseExtractor):
    """
    Cloud Precision PDF Extractor powered by Azure Document Intelligence Layout Model.
    - Sends document to prebuilt-layout API requesting native Markdown output format.
    - Captures complex merged cells, multi-matrix tables, and dense scanned content.
    - Automatically falls back to high-fidelity PDFLocalExtractor if Azure is unconfigured or unavailable.
    """

    def __init__(self):
        self.endpoint = settings.azure_doc_endpoint
        self.key = settings.azure_doc_key
        self.fallback_extractor = PDFLocalExtractor()

    def is_configured(self) -> bool:
        return bool(self.endpoint and self.key)

    async def extract(self, file_bytes: bytes, filename: str, **kwargs) -> ExtractionResult:
        if not self.is_configured():
            logger.warning("Azure Document Intelligence not configured. Falling back to local engine.")
            res = await self.fallback_extractor.extract(file_bytes, filename, **kwargs)
            res.metadata["fallback_reason"] = "cloud_credentials_missing"
            return res

        analyze_url = (
            f"{self.endpoint}/documentintelligence/documentModels/prebuilt-layout:analyze"
            "?api-version=2024-11-30&outputContentFormat=markdown"
        )

        headers = {
            "Ocp-Apim-Subscription-Key": self.key,
            "Content-Type": "application/pdf" if filename.lower().endswith(".pdf") else "application/octet-stream",
        }

        try:
            async with httpx.AsyncClient(timeout=60.0) as client:
                logger.info("Submitting document to Azure Document Intelligence (%s)...", filename)
                resp = await client.post(analyze_url, headers=headers, content=file_bytes)

                if resp.status_code in (400, 404):
                    legacy_url = (
                        f"{self.endpoint}/formrecognizer/documentModels/prebuilt-layout:analyze"
                        "?api-version=2023-07-31&outputContentFormat=markdown"
                    )
                    resp = await client.post(legacy_url, headers=headers, content=file_bytes)

                if resp.status_code != 202:
                    logger.error(
                        "Azure Document Intelligence error: %d %s",
                        resp.status_code,
                        resp.text[:300],
                    )
                    logger.info("Falling back to PDFLocalExtractor...")
                    res = await self.fallback_extractor.extract(file_bytes, filename, **kwargs)
                    res.metadata["fallback_reason"] = f"azure_http_{resp.status_code}"
                    return res

                operation_url = resp.headers.get("Operation-Location")
                if not operation_url:
                    logger.warning("Operation-Location header missing from Azure response")
                    return await self.fallback_extractor.extract(file_bytes, filename, **kwargs)

                poll_headers = {"Ocp-Apim-Subscription-Key": self.key}
                for _ in range(30):
                    await asyncio.sleep(2)
                    poll_resp = await client.get(operation_url, headers=poll_headers)
                    if poll_resp.status_code != 200:
                        continue

                    data = poll_resp.json()
                    status = data.get("status")

                    if status == "succeeded":
                        analyze_result = data.get("analyzeResult", {})
                        markdown_content = analyze_result.get("content", "")
                        pages = analyze_result.get("pages", [])
                        total_pages = len(pages) if pages else 1
                        word_count = len(markdown_content.split())

                        return ExtractionResult(
                            content=markdown_content,
                            images=[],
                            metadata={
                                "filename": filename,
                                "model": "prebuilt-layout",
                                "api_version": "2024-11-30",
                                "total_pages": total_pages,
                                "engine": "cloud",
                            },
                            word_count=word_count,
                            detected_type="pdf",
                        )
                    elif status == "failed":
                        error_msg = data.get("error", {}).get("message", "Unknown error")
                        logger.error("Azure Document Intelligence operation failed: %s", error_msg)
                        break

            logger.warning("Azure Document Intelligence timed out or failed. Falling back to local.")
            return await self.fallback_extractor.extract(file_bytes, filename, **kwargs)

        except Exception as e:
            logger.exception("Error calling Azure Document Intelligence: %s", e)
            res = await self.fallback_extractor.extract(file_bytes, filename, **kwargs)
            res.metadata["fallback_reason"] = str(e)
            return res
