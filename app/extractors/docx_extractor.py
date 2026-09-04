import io
import docx
from typing import List
from app.extractors.base import BaseExtractor, format_markdown_table, format_image_placeholder
from app.models import ExtractionResult, ExtractedImage
from app.assets import default_asset_store
from app.vision import vision_client


class DOCXExtractor(BaseExtractor):
    async def extract(self, file_bytes: bytes, filename: str, **kwargs) -> ExtractionResult:
        doc = docx.Document(io.BytesIO(file_bytes))

        extracted_images: List[ExtractedImage] = []
        image_count = 0

        # Extract images from docx relationships
        for rel_id, rel in doc.part.rels.items():
            if "image" in rel.target_ref:
                image_count += 1
                img_part = rel.target_part
                img_data = img_part.blob
                img_name = f"{filename}_img_{image_count}.png"
                location = f"Embedded Image {image_count}"
                alt_text = f"Embedded Image in {filename}"
                asset_id, url = default_asset_store.save(img_name, "image/png", alt_text, location, img_data)
                extracted_images.append(
                    ExtractedImage(
                        id=asset_id,
                        filename=img_name,
                        content_type="image/png",
                        size_bytes=len(img_data),
                        width=0,
                        height=0,
                        alt_text=alt_text,
                        location=location,
                        relative_path=f"./assets/{img_name}",
                        url=url,
                        data=img_data,
                    )
                )

        if vision_client and vision_client.enabled and extracted_images:
            await vision_client.enrich_images(extracted_images)

        content_parts = []

        # Iterate document elements in document order
        for block in doc.iter_inner_content():
            if isinstance(block, docx.text.paragraph.Paragraph):
                text = block.text.strip()
                if not text:
                    continue

                style_name = block.style.name.lower()
                if "heading 1" in style_name:
                    content_parts.append(f"# {text}")
                elif "heading 2" in style_name:
                    content_parts.append(f"## {text}")
                elif "heading 3" in style_name:
                    content_parts.append(f"### {text}")
                elif "list" in style_name or block.text.startswith(("-", "*", "•")):
                    clean_item = text.lstrip("-*• ").strip()
                    content_parts.append(f"- {clean_item}")
                else:
                    content_parts.append(text)

            elif isinstance(block, docx.table.Table):
                table_rows = []
                for row in block.rows:
                    row_cells = [cell.text.strip().replace("\n", "<br>") for cell in row.cells]
                    table_rows.append(row_cells)

                if table_rows:
                    headers = table_rows[0]
                    rows = table_rows[1:] if len(table_rows) > 1 else []
                    content_parts.append(format_markdown_table(headers, rows))

        final_content = "\n\n".join(content_parts)
        word_count = len(final_content.split())

        return ExtractionResult(
            content=final_content,
            images=extracted_images,
            metadata={
                "filename": filename,
                "paragraphs_and_tables": len(content_parts),
                "engine": "local",
            },
            word_count=word_count,
            detected_type="docx",
        )
