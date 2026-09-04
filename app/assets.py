import hashlib
import mimetypes
from typing import Dict, Optional

class AssetStore:
    def __init__(self):
        self._store: Dict[str, dict] = {}

    def save(self, name: str, mime_type: str, alt_text: str, location: str, data: bytes) -> str:
        asset_id = hashlib.sha256(data).hexdigest()[:24]
        ext = mimetypes.guess_extension(mime_type) or ".png"
        if ext == ".jpe":
            ext = ".jpg"
        url = f"/api/v1/assets/{asset_id}{ext}"
        self._store[asset_id] = {
            "name": name,
            "mime_type": mime_type,
            "alt_text": alt_text,
            "location": location,
            "data": data,
            "url": url,
        }
        return asset_id, url

    def get(self, asset_id: str) -> Optional[dict]:
        clean_id = asset_id.split(".")[0]
        return self._store.get(clean_id)

default_asset_store = AssetStore()
