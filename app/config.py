import os
from dataclasses import dataclass
from dotenv import load_dotenv

load_dotenv()

@dataclass
class Settings:
    port: int = int(os.getenv("PORT", "8080"))
    max_upload_size_mb: int = int(os.getenv("MAX_UPLOAD_SIZE_MB", "32"))
    max_decompressed_size_mb: int = int(os.getenv("MAX_DECOMPRESSED_SIZE_MB", "150"))
    
    # Azure Vision for architecture diagrams & solutioning insights
    enable_ai_vision: bool = os.getenv("ENABLE_AI_VISION", "true").lower() in ("true", "1", "yes")
    azure_vision_endpoint: str = os.getenv("AZURE_VISION_ENDPOINT", "").rstrip("/")
    azure_vision_key: str = os.getenv("AZURE_VISION_KEY", "")
    vision_concurrency: int = int(os.getenv("VISION_CONCURRENCY", "4"))
    vision_timeout_sec: int = int(os.getenv("VISION_TIMEOUT_SEC", "15"))

    # Azure Document Intelligence for Cloud Precision Layout
    # Defaults to the Azure AI Foundry unified resource if not specified
    azure_doc_endpoint: str = os.getenv("AZURE_DOC_ENDPOINT", os.getenv("AZURE_VISION_ENDPOINT", "")).rstrip("/")
    azure_doc_key: str = os.getenv("AZURE_DOC_KEY", os.getenv("AZURE_VISION_KEY", ""))

settings = Settings()
