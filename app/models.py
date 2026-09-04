from dataclasses import dataclass, field
from typing import List, Dict, Any, Optional

@dataclass
class VisionAnalysis:
    tags: List[str] = field(default_factory=list)
    objects: List[str] = field(default_factory=list)
    extracted_text: List[str] = field(default_factory=list)
    summary: str = ""

@dataclass
class ExtractedImage:
    id: str
    filename: str
    content_type: str
    size_bytes: int
    width: int
    height: int
    alt_text: str
    location: str
    relative_path: str
    url: str
    data: bytes = field(repr=False)
    vision_analysis: Optional[VisionAnalysis] = None

@dataclass
class ExtractionResult:
    content: str
    images: List[ExtractedImage] = field(default_factory=list)
    metadata: Dict[str, Any] = field(default_factory=dict)
    word_count: int = 0
    detected_type: str = "unknown"
