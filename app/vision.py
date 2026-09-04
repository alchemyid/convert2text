import asyncio
import httpx
from typing import Dict, Optional, List
from app.config import settings
from app.models import VisionAnalysis, ExtractedImage

def is_vision_candidate(img: ExtractedImage) -> bool:
    loc = img.location.lower()
    if "page 1" in loc or "slide 1" in loc or "cover" in loc:
        return False
    if img.size_bytes < 1500:
        return False
    if img.width > 0 and img.height > 0:
        if img.width < 120 and img.height < 120:
            return False
        aspect = float(img.width) / float(img.height)
        if aspect > 5.0 or aspect < 0.2:
            return False
    return True

class AzureVisionClient:
    def __init__(self):
        self.endpoint = settings.azure_vision_endpoint
        self.key = settings.azure_vision_key
        self.enabled = settings.enable_ai_vision and bool(self.endpoint) and bool(self.key)
        self.concurrency = settings.vision_concurrency
        self.timeout = settings.vision_timeout_sec

    async def analyze_image(self, data: bytes) -> Optional[VisionAnalysis]:
        if not self.enabled:
            return None

        url = f"{self.endpoint}/computervision/imageanalysis:analyze?api-version=2024-02-01&features=tags,objects,read,caption"
        headers = {
            "Ocp-Apim-Subscription-Key": self.key,
            "Content-Type": "application/octet-stream",
        }

        try:
            async with httpx.AsyncClient(timeout=self.timeout) as client:
                resp = await client.post(url, headers=headers, content=data)
                if resp.status_code != 200:
                    return None
                result = resp.json()

            tags = [t["name"] for t in result.get("tagsResult", {}).get("values", []) if t.get("confidence", 0) >= 0.5]
            objects = [o["name"] for o in result.get("objectsResult", {}).get("values", [])]
            
            extracted_text = []
            for block in result.get("readResult", {}).get("blocks", []):
                for line in block.get("lines", []):
                    extracted_text.append(line.get("text", ""))

            summary = result.get("captionResult", {}).get("text", "")

            return VisionAnalysis(
                tags=tags,
                objects=objects,
                extracted_text=extracted_text,
                summary=summary,
            )
        except Exception:
            return None

    async def enrich_images(self, images: List[ExtractedImage]):
        candidates = [img for img in images if is_vision_candidate(img)]
        if not candidates:
            return

        semaphore = asyncio.Semaphore(self.concurrency)

        async def worker(img: ExtractedImage):
            async with semaphore:
                analysis = await self.analyze_image(img.data)
                img.vision_analysis = analysis

        await asyncio.gather(*(worker(img) for img in candidates))

vision_client = AzureVisionClient()
