from abc import ABC, abstractmethod
from typing import List, Dict, Any, Optional
from app.models import ExtractionResult, ExtractedImage, VisionAnalysis


def format_markdown_table(headers: List[str], rows: List[List[str]]) -> str:
    if not headers and not rows:
        return ""

    num_cols = max(len(headers), max((len(r) for r in rows), default=0))
    if num_cols == 0:
        return ""

    norm_headers = [str(h).strip().replace("\n", " ") for h in headers] + [""] * (num_cols - len(headers))
    norm_rows = []
    for r in rows:
        norm_r = [str(c).strip().replace("\n", "<br>") for c in r] + [""] * (num_cols - len(r))
        norm_rows.append(norm_r)

    col_widths = [max(len(norm_headers[c]), 3) for c in range(num_cols)]
    for r in norm_rows:
        for c in range(num_cols):
            col_widths[c] = max(col_widths[c], len(r[c]))

    def render_row(cells: List[str]) -> str:
        padded = [cells[c].ljust(col_widths[c]) for c in range(num_cols)]
        return "| " + " | ".join(padded) + " |"

    separator = "| " + " | ".join(["-" * w for w in col_widths]) + " |"

    lines = [render_row(norm_headers), separator]
    for r in norm_rows:
        lines.append(render_row(r))

    return "\n".join(lines)


def format_image_placeholder(img: ExtractedImage) -> str:
    rel_path = img.relative_path or f"./assets/{img.filename}"
    res = f"> 🖼️ **[IMAGE: {img.filename} ({img.location})]**\n"
    res += f"> - **Description / Alt-Text**: {img.alt_text}\n"
    if img.vision_analysis:
        va = img.vision_analysis
        res += "> - **AI Vision Analysis (Solutioning & Architecture Insights)**:\n"
        if va.summary:
            res += f">   - **Summary**: {va.summary}\n"
        if va.tags:
            res += f">   - **Detected Tech / Tags**: {', '.join(va.tags)}\n"
        if va.objects:
            res += f">   - **Identified Entities**: {', '.join(va.objects)}\n"
        if va.extracted_text:
            res += f">   - **Diagram OCR Text**: {' | '.join(va.extracted_text[:10])}\n"
    res += f"![{img.filename}]({rel_path})\n"
    return res


class BaseExtractor(ABC):
    @abstractmethod
    async def extract(self, file_bytes: bytes, filename: str, **kwargs) -> ExtractionResult:
        pass
