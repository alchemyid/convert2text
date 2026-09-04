import io
import logging
from typing import List, Tuple, Optional
import pymupdf  # Modern PyMuPDF API
import pdfplumber

from app.models import ExtractionResult, ExtractedImage
from app.extractors.base import BaseExtractor, format_markdown_table, format_image_placeholder
from app.assets import default_asset_store
from app.vision import vision_client

logger = logging.getLogger(__name__)


def _intersects_table(
    block_bbox: Tuple[float, float, float, float],
    table_bboxes: List[Tuple[float, float, float, float]],
    threshold: float = 0.20,
) -> bool:
    """Checks if a text block substantially overlaps with any table bounding box."""
    bx0, by0, bx1, by1 = block_bbox
    b_width = bx1 - bx0
    b_height = by1 - by0
    b_area = b_width * b_height
    if b_area <= 0:
        return False

    for tx0, ty0, tx1, ty1 in table_bboxes:
        ix0 = max(bx0, tx0)
        iy0 = max(by0, ty0)
        ix1 = min(bx1, tx1)
        iy1 = min(by1, ty1)
        if ix1 > ix0 and iy1 > iy0:
            i_area = (ix1 - ix0) * (iy1 - iy0)
            if i_area / b_area >= threshold or i_area > 40:
                return True
    return False


def _normalize_table_grid(
    raw_rows: List[List[Optional[str]]],
    last_headers: Optional[List[str]] = None,
) -> Tuple[List[str], List[List[str]]]:
    """
    Dynamically normalizes table cells without any hardcoding:
    - Cleans whitespace and unicode bullets (e.g. \uf0b7 -> •)
    - Prunes columns that are 100% blank
    - Merges multi-row table headers
    - Merges split header/data columns
    - Propagates headers to multi-page continuation tables
    """
    if not raw_rows:
        return [], []

    cleaned = []
    for r in raw_rows:
        cleaned_r = []
        for c in r:
            if c is None:
                cleaned_r.append("")
            else:
                s = str(c).replace("\uf0b7", "•").replace("\r\n", "\n").replace("\r", "\n").strip()
                s = s.replace("|", "\\|")
                # Format multiline inside cell as <br>
                lines = [line.strip() for line in s.split("\n") if line.strip()]
                cleaned_r.append("<br>".join(lines))
        cleaned.append(cleaned_r)

    # Filter completely empty rows
    cleaned = [r for r in cleaned if any(c != "" for c in r)]
    if not cleaned:
        return [], []

    max_cols = max(len(r) for r in cleaned)
    padded = [r + [""] * (max_cols - len(r)) for r in cleaned]

    # Remove columns that are 100% empty across all rows
    non_empty_cols = [c for c in range(max_cols) if any(padded[r][c] != "" for r in range(len(padded)))]
    filtered = [[r[c] for c in non_empty_cols] for r in padded]
    if not filtered or not filtered[0]:
        return [], []
    num_cols = len(filtered[0])

    # Check if first row is data (continuation table from previous page)
    col0 = filtered[0][0].strip()
    is_continuation = col0.isdigit()

    if is_continuation and last_headers and len(last_headers) == num_cols:
        return last_headers, filtered

    # Determine header depth
    data_start = 1
    for i in range(1, min(4, len(filtered))):
        r_col0 = filtered[i][0].strip()
        if r_col0.isdigit() or (len(r_col0) > 0 and r_col0[0].isdigit()):
            data_start = i
            break
        if not r_col0 and any(filtered[i]):
            data_start = i + 1

    header_rows = filtered[:data_start]
    data_rows = filtered[data_start:]

    merged_headers = []
    for c in range(num_cols):
        parts = [header_rows[r][c] for r in range(len(header_rows)) if header_rows[r][c]]
        merged_headers.append(" ".join(parts))

    # Resolve split header/data columns
    merged_map = {}
    for c in range(num_cols - 1):
        c_has_data = any(dr[c] for dr in data_rows)
        c_has_hdr = bool(merged_headers[c])
        next_has_data = any(dr[c + 1] for dr in data_rows)
        next_has_hdr = bool(merged_headers[c + 1])

        if c_has_data and not c_has_hdr and not next_has_data and next_has_hdr:
            merged_headers[c] = merged_headers[c + 1]
            merged_map[c + 1] = c
        elif not c_has_data and c_has_hdr and next_has_data and not next_has_hdr:
            merged_headers[c + 1] = merged_headers[c]
            merged_map[c] = c + 1

    kept = [c for c in range(num_cols) if c not in merged_map]
    final_headers = [merged_headers[c] for c in kept]
    raw_data_rows = [[dr[c] for c in kept] for dr in data_rows]

    # Merge multi-line continuation rows where column 0 is empty
    merged_rows: List[List[str]] = []
    for r in raw_data_rows:
        col0 = r[0].strip()
        # If col0 is empty, but row has content, merge into previous row
        if not col0 and any(c.strip() for c in r) and merged_rows:
            prev = merged_rows[-1]
            for c_idx in range(len(r)):
                if r[c_idx].strip():
                    if prev[c_idx].strip():
                        prev[c_idx] += "<br>" + r[c_idx].strip()
                    else:
                        prev[c_idx] = r[c_idx].strip()
        else:
            merged_rows.append(list(r))

    # Merge dedicated bullet-only columns with the adjacent text column
    num_kept = len(final_headers)
    bullet_merge_map = {}
    for c in range(num_kept - 1):
        is_bullet_col = all(
            all(part.strip() in ("•", "-") or not part.strip() for part in r[c].split("<br>"))
            for r in merged_rows
        ) and any(r[c].strip() for r in merged_rows)
        if is_bullet_col:
            bullet_merge_map[c] = c + 1

    if bullet_merge_map:
        non_bullet_indices = [c for c in range(num_kept) if c not in bullet_merge_map]
        clean_final_headers = [final_headers[c] for c in non_bullet_indices]
        clean_final_rows = []
        for r in merged_rows:
            row_copy = list(r)
            for bullet_c, text_c in bullet_merge_map.items():
                if row_copy[text_c]:
                    lines = [l.strip() for l in row_copy[text_c].split("<br>") if l.strip()]
                    row_copy[text_c] = "<br>".join(
                        ["• " + l if not l.startswith("•") else l for l in lines]
                    )
            clean_final_rows.append([row_copy[c] for c in non_bullet_indices])
    # Merge columns that have no header into the previous column
    new_headers = []
    col_mapping = []
    curr_new_idx = -1
    for c, h in enumerate(final_headers):
        if not h.strip() and curr_new_idx >= 0:
            col_mapping.append(curr_new_idx)
        else:
            curr_new_idx += 1
            new_headers.append(h)
            col_mapping.append(curr_new_idx)

    if len(new_headers) < len(final_headers):
        unnamed_merged_rows = []
        for r in merged_rows:
            merged_r = [""] * len(new_headers)
            for old_c, val in enumerate(r):
                target_c = col_mapping[old_c]
                v = val.strip()
                if not v:
                    continue
                if not merged_r[target_c]:
                    merged_r[target_c] = v
                else:
                    if set(merged_r[target_c].replace("<br>", "").strip()) <= {"•", "-"}:
                        bullets = [b for b in merged_r[target_c].split("<br>") if b.strip()]
                        lines = [l for l in v.split("<br>") if l.strip()]
                        combined = []
                        for i in range(max(len(bullets), len(lines))):
                            b = bullets[i] if i < len(bullets) else "•"
                            l = lines[i] if i < len(lines) else ""
                            combined.append(f"{b} {l}".strip())
                        merged_r[target_c] = "<br>".join(combined)
                    elif v not in merged_r[target_c]:
                        merged_r[target_c] += "<br>" + v
            unnamed_merged_rows.append(merged_r)
        final_headers = new_headers
        merged_rows = unnamed_merged_rows

    return final_headers, merged_rows


class PDFLocalExtractor(BaseExtractor):
    """
    High-fidelity Local PDF Extractor:
    - pdfplumber: physical line-based table detection and dynamic cell extraction
    - PyMuPDF (pymupdf): font decoding (italic, bold, symbols), text layout, and images
    - Zero hardcoded headers or coordinates
    - Fully local, 100% private, Rp 0 token cost
    """

    async def extract(self, file_bytes: bytes, filename: str, **kwargs) -> ExtractionResult:
        fitz_doc = pymupdf.open(stream=file_bytes, filetype="pdf")
        pdfplumber_doc = pdfplumber.open(io.BytesIO(file_bytes))

        total_pages = len(fitz_doc)
        md_parts: List[str] = []
        extracted_images: List[ExtractedImage] = []
        image_count = 0
        last_table_headers: Optional[List[str]] = None

        try:
            for page_idx in range(total_pages):
                fitz_page = fitz_doc[page_idx]
                plumber_page = pdfplumber_doc.pages[page_idx]
                page_num = page_idx + 1

                # 1. Detect open-topped continuation tables across page boundaries
                initial_tables = plumber_page.find_tables()
                explicit_lines = []
                if initial_tables:
                    t_top = min(t.bbox[1] for t in initial_tables)
                    v_edges = [
                        e
                        for e in plumber_page.edges
                        if abs(e["x0"] - e["x1"]) < 1 and e["top"] < t_top - 5
                    ]
                    if v_edges:
                        min_y = min(e["top"] for e in v_edges)
                        explicit_lines.append(min_y)

                table_settings = {
                    "vertical_strategy": "lines",
                    "horizontal_strategy": "lines",
                    "snap_tolerance": 4,
                    "join_tolerance": 4,
                }
                if explicit_lines:
                    table_settings["explicit_horizontal_lines"] = explicit_lines

                detected_tables = plumber_page.find_tables(table_settings=table_settings)

                # Filter enclosed / nested sub-tables
                outer_tables = []
                for t in detected_tables:
                    bx0, by0, bx1, by1 = t.bbox
                    is_enclosed = False
                    for other in detected_tables:
                        if t == other:
                            continue
                        ox0, oy0, ox1, oy1 = other.bbox
                        if (
                            ox0 <= bx0 + 2
                            and oy0 <= by0 + 2
                            and ox1 >= bx1 - 2
                            and oy1 >= by1 - 2
                        ):
                            is_enclosed = True
                            break
                    if not is_enclosed:
                        outer_tables.append(t)

                table_bboxes: List[Tuple[float, float, float, float]] = []
                page_elements: List[Tuple[float, str, str]] = []

                # Process outer tables
                for t in outer_tables:
                    bbox = t.bbox
                    table_bboxes.append(bbox)
                    raw_data = t.extract()
                    if not raw_data:
                        continue

                    headers, rows = _normalize_table_grid(raw_data, last_table_headers)
                    if headers:
                        last_table_headers = headers
                        table_md = format_markdown_table(headers, rows)
                        page_elements.append((bbox[1], "table", table_md))

                # 2. Extract non-table text blocks from PyMuPDF
                text_blocks = fitz_page.get_text("blocks")
                page_height = fitz_page.rect.height

                for block in text_blocks:
                    bx0, by0, bx1, by1, text, block_no, block_type = block
                    if block_type != 0:
                        continue

                    cleaned_text = text.strip()
                    if not cleaned_text:
                        continue

                    # Filter out running header/footer if at extreme top/bottom
                    if (by1 < 35 or by0 > page_height - 35) and len(cleaned_text) < 30:
                        continue

                    # Skip text block if it falls inside any table bbox
                    if _intersects_table((bx0, by0, bx1, by1), table_bboxes):
                        continue

                    page_elements.append((by0, "text", cleaned_text))

                # 3. Extract candidate diagrams / architecture images (skip page 1 cover)
                if page_num > 1:
                    images = fitz_page.get_images(full=True)
                    for img_info in images:
                        xref = img_info[0]
                        base_img = fitz_doc.extract_image(xref)
                        if not base_img:
                            continue

                        img_bytes = base_img["image"]
                        img_ext = base_img["ext"]
                        width = base_img["width"]
                        height = base_img["height"]

                        # Filter out small icons or extreme aspect ratios
                        if width < 180 or height < 140:
                            continue

                        aspect = width / max(height, 1)
                        if aspect > 4.0 or aspect < 0.25:
                            continue

                        # Check vertical position: avoid running header or footer logos
                        img_rects = fitz_page.get_image_rects(xref)
                        img_y = img_rects[0].y0 if img_rects else page_height / 2
                        if img_y < 70 or img_y > page_height - 70:
                            continue

                        image_count += 1
                        mime_type = f"image/{img_ext}"
                        if img_ext == "jpg":
                            mime_type = "image/jpeg"
                        img_name = f"{filename}_p{page_num}_img_{image_count}.{img_ext}"
                        location = f"Page {page_num}"
                        alt_text = f"Diagram / Figure on Page {page_num}"

                        asset_id, url = default_asset_store.save(
                            img_name, mime_type, alt_text, location, img_bytes
                        )

                        extracted_img = ExtractedImage(
                            id=asset_id,
                            filename=img_name,
                            content_type=mime_type,
                            size_bytes=len(img_bytes),
                            width=width,
                            height=height,
                            alt_text=alt_text,
                            location=location,
                            relative_path=f"./assets/{img_name}",
                            url=url,
                            data=img_bytes,
                        )
                        extracted_images.append(extracted_img)
                        page_elements.append(
                            (img_y, "image", format_image_placeholder(extracted_img))
                        )

                # 4. Sort all elements on page top-to-bottom for natural reading flow
                page_elements.sort(key=lambda item: item[0])

                page_md_content = "\n\n".join(elem[2] for elem in page_elements if elem[2].strip())
                if page_md_content:
                    if total_pages > 1:
                        md_parts.append(f"<!-- Page {page_num} -->\n{page_md_content}")
                    else:
                        md_parts.append(page_md_content)

        finally:
            fitz_doc.close()
            pdfplumber_doc.close()

        # Enrich candidate images with Azure Vision AI if configured
        if vision_client and vision_client.enabled and extracted_images:
            logger.info("Enriching %d candidate images with Azure Vision AI...", len(extracted_images))
            await vision_client.enrich_images(extracted_images)

        final_markdown = "\n\n---\n\n".join(md_parts)
        word_count = len(final_markdown.split())

        return ExtractionResult(
            content=final_markdown,
            images=extracted_images,
            metadata={
                "filename": filename,
                "total_pages": total_pages,
                "engine": "local",
            },
            word_count=word_count,
            detected_type="pdf",
        )
