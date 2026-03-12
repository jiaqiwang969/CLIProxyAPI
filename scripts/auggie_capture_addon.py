from __future__ import annotations

import json
import os
import time
from pathlib import Path

from mitmproxy import ctx, http


OUTPUT_DIR = Path(os.environ.get("AUGGIE_CAPTURE_DIR", "/tmp/auggie-capture")).expanduser()
OUTPUT_DIR.mkdir(parents=True, exist_ok=True)


class AuggieCaptureAddon:
    def __init__(self) -> None:
        self.counter = 0

    def load(self, loader) -> None:
        ctx.log.info(f"auggie capture output dir: {OUTPUT_DIR}")

    def response(self, flow: http.HTTPFlow) -> None:
        self.counter += 1
        flow_id = f"{self.counter:04d}"
        started_at = int(time.time())
        base_name = f"{flow_id}_{started_at}"

        req_body = flow.request.raw_content or b""
        resp_body = flow.response.raw_content or b""

        record = {
            "id": flow_id,
            "timestamp": started_at,
            "request": {
                "method": flow.request.method,
                "scheme": flow.request.scheme,
                "host": flow.request.host,
                "port": flow.request.port,
                "path": flow.request.path,
                "pretty_url": flow.request.pretty_url,
                "headers": dict(flow.request.headers),
                "body_file": f"{base_name}.request.bin",
                "body_size": len(req_body),
            },
            "response": {
                "status_code": flow.response.status_code,
                "reason": flow.response.reason,
                "headers": dict(flow.response.headers),
                "body_file": f"{base_name}.response.bin",
                "body_size": len(resp_body),
            },
        }

        (OUTPUT_DIR / f"{base_name}.json").write_text(
            json.dumps(record, ensure_ascii=False, indent=2),
            encoding="utf-8",
        )
        (OUTPUT_DIR / f"{base_name}.request.bin").write_bytes(req_body)
        (OUTPUT_DIR / f"{base_name}.response.bin").write_bytes(resp_body)


addons = [AuggieCaptureAddon()]
