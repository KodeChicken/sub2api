#!/usr/bin/env python3
"""Generate one image through a Sub2API OpenAI-compatible Image API."""

from __future__ import annotations

import argparse
import base64
import io
import json
import mimetypes
import os
import re
import struct
import sys
import tempfile
import uuid
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from collections.abc import Iterable, Iterator
from typing import Any


DEFAULT_MODEL = "gpt-image-2"
DEFAULT_SIZE = "2048x1152"
DEFAULT_QUALITY = "high"
QUALITIES = ("low", "medium", "high", "auto")
FORMATS = ("png", "jpeg", "webp")
SIZE_RE = re.compile(r"^(\d+)x(\d+)$")


class SkillError(RuntimeError):
    pass


def codex_home() -> Path:
    return Path(os.environ.get("CODEX_HOME", Path.home() / ".codex")).expanduser()


def load_json(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise SkillError(f"Cannot read valid JSON from {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise SkillError(f"Expected a JSON object in {path}")
    return value


def resolve_api_key(required: bool = True) -> tuple[str | None, str]:
    value = os.environ.get("OPENAI_API_KEY", "").strip()
    if value:
        return value, "env:OPENAI_API_KEY"

    auth_path = codex_home() / "auth.json"
    if auth_path.is_file():
        value = str(load_json(auth_path).get("OPENAI_API_KEY", "")).strip()
        if value:
            return value, str(auth_path)

    if required:
        raise SkillError(
            "OPENAI_API_KEY is unavailable. Set it in the environment or in "
            f"{auth_path} as the top-level OPENAI_API_KEY field."
        )
    return None, "unavailable"


def load_codex_base_url() -> str:
    config_path = codex_home() / "config.toml"
    if not config_path.is_file():
        return ""
    try:
        import tomllib

        config = tomllib.loads(config_path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        raise SkillError(f"Cannot parse {config_path}: {exc}") from exc

    provider_id = str(config.get("model_provider", "")).strip()
    providers = config.get("model_providers", {})
    if not provider_id or not isinstance(providers, dict):
        return ""
    provider = providers.get(provider_id, {})
    if not isinstance(provider, dict):
        return ""
    return str(provider.get("base_url", "")).strip()


def endpoint_from_base_url(base_url: str, editing: bool = False) -> str:
    value = base_url.strip().rstrip("/")
    if not value:
        raise SkillError(
            "Sub2API base URL is unavailable. Use --base-url, OPENAI_BASE_URL, "
            "or configure the selected Codex model provider's base_url."
        )
    parsed = urllib.parse.urlparse(value)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise SkillError(f"Invalid base URL: {value}")
    for suffix in ("/v1/images/generations", "/v1/images/edits"):
        if value.endswith(suffix):
            value = value[: -len(suffix)]
            break
    if value.endswith("/v1"):
        return value + ("/images/edits" if editing else "/images/generations")
    return value + ("/v1/images/edits" if editing else "/v1/images/generations")


def resolve_endpoint(cli_base_url: str | None, editing: bool = False) -> tuple[str, str]:
    if cli_base_url:
        return endpoint_from_base_url(cli_base_url, editing), "argument:--base-url"
    env_url = os.environ.get("OPENAI_BASE_URL", "").strip()
    if env_url:
        return endpoint_from_base_url(env_url, editing), "env:OPENAI_BASE_URL"
    config_url = load_codex_base_url()
    return endpoint_from_base_url(config_url, editing), str(codex_home() / "config.toml")


def parse_size(value: str, model: str) -> tuple[int, int] | None:
    if value == "auto":
        return None
    match = SIZE_RE.fullmatch(value)
    if not match:
        raise SkillError("--size must be auto or WIDTHxHEIGHT")
    width, height = int(match.group(1)), int(match.group(2))
    if model.startswith(("gpt-image-2", "gpt-image-2.5")):
        pixels = width * height
        if max(width, height) > 3840:
            raise SkillError(f"{model} requires each edge to be at most 3840px")
        if width % 16 or height % 16:
            raise SkillError(f"{model} requires both edges to be multiples of 16")
        if max(width, height) / min(width, height) > 3:
            raise SkillError(f"{model} requires an aspect ratio no wider than 3:1")
        if not 655_360 <= pixels <= 8_294_400:
            raise SkillError(f"{model} requires pixel count 655360..8294400")
    elif model.startswith("dall-e-2") and value not in {"256x256", "512x512", "1024x1024"}:
        raise SkillError(f"{model} does not support size {value}")
    elif model.startswith("dall-e-3") and value not in {"1024x1024", "1792x1024", "1024x1792"}:
        raise SkillError(f"{model} does not support size {value}")
    elif value not in {"1024x1024", "1536x1024", "1024x1536"}:
        raise SkillError(f"{model} does not support flexible size {value}")
    return width, height


ASPECT_SIZES = {
    "1:1": "2048x2048", "3:2": "1536x1024", "2:3": "1024x1536",
    "16:9": "2048x1152", "9:16": "1152x2048",
}


def selected_size(size: str | None, aspect_ratio: str | None, model: str) -> str:
    if aspect_ratio and aspect_ratio not in ASPECT_SIZES:
        raise SkillError("--aspect-ratio must be 1:1, 3:2, 2:3, 16:9, or 9:16")
    if aspect_ratio and not model.startswith(("gpt-image-2", "gpt-image-2.5")):
        raise SkillError("--aspect-ratio is available only for GPT Image 2 models")
    value = size or (ASPECT_SIZES[aspect_ratio] if aspect_ratio else DEFAULT_SIZE if model.startswith("gpt-image-2") else "1024x1024")
    dimensions = parse_size(value, model)
    if aspect_ratio and dimensions:
        ratio = ASPECT_SIZES[aspect_ratio].split("x")
        if abs(dimensions[0] / dimensions[1] - int(ratio[0]) / int(ratio[1])) > 0.005:
            raise SkillError("--size conflicts with --aspect-ratio")
    return value


def validate_parameters(args: argparse.Namespace, editing: bool) -> None:
    model = args.model.lower()
    if not (model.startswith("gpt-image-") or model.startswith("dall-e-")):
        raise SkillError("Unsupported image model; use a GPT Image or DALL-E model")
    if args.n < 1 or args.n > 10 or (model.startswith("dall-e-3") and args.n != 1):
        raise SkillError("--n must be 1..10 (DALL-E 3 requires 1)")
    if args.mask and not editing:
        raise SkillError("--mask requires at least one --image")
    if editing and model.startswith("dall-e-3"):
        raise SkillError("DALL-E 3 does not support image edits")
    if editing and len(args.image) > (16 if model.startswith("gpt-image-") else 1):
        raise SkillError("Too many reference images for this model")
    for image in args.image:
        if Path(image).suffix.lower() not in {".png", ".jpg", ".jpeg", ".webp"} or not Path(image).is_file():
            raise SkillError(f"Reference image must be an existing PNG, JPEG, or WebP: {image}")
    if args.mask and (Path(args.mask).suffix.lower() != ".png" or not Path(args.mask).is_file()):
        raise SkillError("--mask must be an existing PNG file")
    if args.input_fidelity and not editing:
        raise SkillError("--input-fidelity is edit-only")
    if model.startswith("dall-e-"):
        if args.background or args.output_format or args.output_compression is not None or args.moderation or args.partial_images is not None or args.input_fidelity:
            raise SkillError("GPT Image-only parameters are not supported by DALL-E")
        if not args.no_stream:
            raise SkillError("DALL-E does not support streaming; use --no-stream")
    elif args.style or args.response_format:
        raise SkillError("--style and --response-format are DALL-E-only")
    if args.style and not model.startswith("dall-e-3"):
        raise SkillError("--style is DALL-E 3-only")
    if args.quality in {"xhigh", "max"} and not model.startswith("gpt-image-2.5"):
        raise SkillError("--quality xhigh/max requires a GPT Image 2.5 model")
    if model.startswith("dall-e-3") and args.quality not in {None, "standard", "hd"}:
        raise SkillError("DALL-E 3 quality must be standard or hd")
    if model.startswith("dall-e-2") and args.quality is not None:
        raise SkillError("DALL-E 2 does not support --quality")
    if model.startswith("gpt-image-") and args.quality in {"standard", "hd"}:
        raise SkillError("GPT Image quality must be auto, low, medium, high, xhigh, or max")
    if args.output_compression is not None and (not 0 <= args.output_compression <= 100 or args.output_format not in {"jpeg", "webp"}):
        raise SkillError("--output-compression 0..100 requires jpeg or webp")
    if args.background == "transparent" and args.output_format == "jpeg":
        raise SkillError("Transparent background cannot be JPEG")
    if args.partial_images is not None and (args.no_stream or not 0 <= args.partial_images <= 3):
        raise SkillError("--partial-images 0..3 requires streaming")


def request_payload(args: argparse.Namespace, prompt: str, size: str, editing: bool) -> dict[str, Any]:
    payload: dict[str, Any] = {"model": args.model, "prompt": prompt, "n": args.n, "size": size}
    if not args.model.startswith("dall-e-2"):
        payload["quality"] = args.quality or ("standard" if args.model.startswith("dall-e-3") else DEFAULT_QUALITY)
    if args.model.startswith("dall-e-"):
        if args.style: payload["style"] = args.style
        if args.response_format: payload["response_format"] = args.response_format
    else:
        payload.update(stream=not args.no_stream)
        for key in ("background", "output_format", "output_compression", "moderation", "partial_images"):
            value = getattr(args, key)
            if value is not None: payload[key] = value
        if editing and args.input_fidelity: payload["input_fidelity"] = args.input_fidelity
    if args.user: payload["user"] = args.user
    return payload


def prompt_from_args(args: argparse.Namespace) -> str:
    if args.prompt_file:
        try:
            prompt = Path(args.prompt_file).read_text(encoding="utf-8")
        except OSError as exc:
            raise SkillError(f"Cannot read prompt file {args.prompt_file}: {exc}") from exc
    else:
        prompt = args.prompt or ""
    prompt = prompt.strip()
    if not prompt:
        raise SkillError("Prompt must not be empty")
    return prompt


def image_dimensions(data: bytes) -> tuple[int, int] | None:
    if data.startswith(b"\x89PNG\r\n\x1a\n") and len(data) >= 24:
        return struct.unpack(">II", data[16:24])
    try:
        from PIL import Image

        with Image.open(io.BytesIO(data)) as image:
            return image.size
    except (ImportError, OSError):
        return None


def resize_exact(data: bytes, size: tuple[int, int], output_format: str) -> bytes:
    try:
        from PIL import Image, ImageOps
    except ImportError as exc:
        raise SkillError(
            "--exact-size requires Pillow. Install it in the active environment: "
            "python -m pip install pillow"
        ) from exc

    with Image.open(io.BytesIO(data)) as image:
        if output_format == "jpeg":
            image = image.convert("RGB")
        fitted = ImageOps.fit(image, size, method=Image.Resampling.LANCZOS)
        buffer = io.BytesIO()
        save_format = {"png": "PNG", "jpeg": "JPEG", "webp": "WEBP"}[output_format]
        fitted.save(buffer, format=save_format)
        return buffer.getvalue()


def require_pillow() -> None:
    try:
        import PIL  # noqa: F401
    except ImportError as exc:
        raise SkillError(
            "--exact-size requires Pillow. Install it in the active environment: "
            "python -m pip install pillow"
        ) from exc


def safe_error_body(raw: bytes) -> str:
    text = raw.decode("utf-8", errors="replace")[:4000]
    try:
        return json.dumps(json.loads(text), ensure_ascii=False)
    except json.JSONDecodeError:
        return text


def decode_base64_image(encoded: Any, context: str) -> bytes:
    if not isinstance(encoded, str) or not encoded:
        raise SkillError(f"{context} is missing b64_json")
    try:
        return base64.b64decode(encoded, validate=True)
    except (ValueError, base64.binascii.Error) as exc:
        raise SkillError(f"{context} contains invalid Base64 image data") from exc


def image_from_response(item: dict[str, Any], context: str, endpoint: str = "", api_key: str = "") -> bytes:
    if item.get("b64_json"):
        return decode_base64_image(item["b64_json"], context)
    url = item.get("url")
    if isinstance(url, str) and url:
        target = urllib.parse.urljoin(endpoint, url)
        parsed = urllib.parse.urlparse(target)
        origin = urllib.parse.urlparse(endpoint)
        if parsed.scheme not in {"http", "https"} or not parsed.netloc:
            raise SkillError(f"{context} contains an invalid image URL")
        headers = {"Authorization": f"Bearer {api_key}"} if parsed.netloc == origin.netloc and api_key else {}
        request = urllib.request.Request(target, headers=headers)
        with urllib.request.urlopen(request, timeout=60) as response:
            data = response.read((25 << 20) + 1)
            if len(data) > 25 << 20:
                raise SkillError(f"{context} image exceeds 25 MB")
            return data
    raise SkillError(f"{context} is missing image data")


def parse_json_image_response(raw: bytes, status: int, endpoint: str = "", api_key: str = "") -> tuple[list[bytes], dict[str, Any]]:
    try:
        result = json.loads(raw)
    except json.JSONDecodeError as exc:
        raise SkillError(f"HTTP {status} returned non-JSON content") from exc
    if not isinstance(result, dict):
        raise SkillError(f"HTTP {status} returned a non-object JSON response")
    data = result.get("data")
    if not isinstance(data, list) or not data or not all(isinstance(item, dict) for item in data):
        fields = sorted(result.keys())
        raise SkillError(
            f"HTTP {status} response is missing data images; "
            f"top-level fields: {fields}"
        )
    return [image_from_response(item, f"data[{index}]", endpoint, api_key) for index, item in enumerate(data)], result


def iter_sse_events(lines: Iterable[bytes]) -> Iterator[tuple[str, bytes]]:
    event_name = ""
    data_lines: list[str] = []

    def emit() -> tuple[str, bytes] | None:
        if not data_lines:
            return None
        return event_name, "\n".join(data_lines).encode("utf-8")

    for raw_line in lines:
        line = raw_line.decode("utf-8", errors="replace").rstrip("\r\n")
        if not line:
            event = emit()
            if event is not None:
                yield event
            event_name = ""
            data_lines = []
            continue
        if line.startswith(":"):
            continue
        field, separator, value = line.partition(":")
        if not separator:
            continue
        if value.startswith(" "):
            value = value[1:]
        if field == "event":
            event_name = value
        elif field == "data":
            data_lines.append(value)

    event = emit()
    if event is not None:
        yield event


def stream_error_message(event: dict[str, Any]) -> str:
    error = event.get("error")
    if isinstance(error, dict) and isinstance(error.get("message"), str):
        return error["message"]
    if isinstance(error, str) and error:
        return error
    if isinstance(event.get("message"), str) and event["message"]:
        return event["message"]
    return f"fields={sorted(event.keys())}"


def parse_stream_image_response(
    lines: Iterable[bytes], status: int, endpoint: str = "", api_key: str = ""
) -> tuple[list[bytes], dict[str, Any]]:
    partial_images = 0
    images: list[bytes] = []
    last_event: dict[str, Any] = {}
    for event_name, raw_data in iter_sse_events(lines):
        if raw_data.strip() == b"[DONE]":
            continue
        try:
            event = json.loads(raw_data)
        except json.JSONDecodeError as exc:
            raise SkillError(f"HTTP {status} returned invalid SSE JSON") from exc
        if not isinstance(event, dict):
            raise SkillError(f"HTTP {status} returned a non-object SSE event")

        event_type = str(event.get("type") or event_name).strip()
        if event_type.endswith(".partial_image"):
            partial_images += 1
            continue
        if event_type == "error" or event_name == "error":
            raise SkillError(
                f"HTTP {status} stream error: {stream_error_message(event)}"
            )
        if event_type in {"image_generation.completed", "image_edit.completed"}:
            images.append(image_from_response(event, event_type, endpoint, api_key))
            last_event = event

    if not images:
        raise SkillError(f"HTTP {status} stream ended before image completion")
    last_event["partial_images_received"] = partial_images
    return images, last_event


def multipart_body(payload: dict[str, Any], images: list[str], mask: str | None) -> tuple[bytes, str]:
    boundary = "----sub2api-imagegen-" + uuid.uuid4().hex
    body = io.BytesIO()
    for key, value in payload.items():
        body.write(f"--{boundary}\r\nContent-Disposition: form-data; name=\"{key}\"\r\n\r\n{str(value).lower() if isinstance(value, bool) else value}\r\n".encode("utf-8"))
    for name, paths in (("image[]", images), ("mask", [mask] if mask else [])):
        for value in paths:
            path = Path(value).expanduser()
            data = path.read_bytes()
            if len(data) > 25 << 20:
                raise SkillError(f"Image exceeds 25 MB: {path.name}")
            mime = mimetypes.guess_type(path.name)[0] or "application/octet-stream"
            body.write(f"--{boundary}\r\nContent-Disposition: form-data; name=\"{name}\"; filename=\"{path.name}\"\r\nContent-Type: {mime}\r\n\r\n".encode("utf-8"))
            body.write(data)
            body.write(b"\r\n")
    body.write(f"--{boundary}--\r\n".encode())
    return body.getvalue(), f"multipart/form-data; boundary={boundary}"


def call_image_api(
    endpoint: str,
    api_key: str,
    payload: dict[str, Any],
    timeout: int,
    stream: bool,
    images: list[str] | None = None,
    mask: str | None = None,
) -> tuple[list[bytes], dict[str, Any]]:
    body, content_type = multipart_body(payload, images, mask) if images else (json.dumps(payload, ensure_ascii=False).encode("utf-8"), "application/json")
    request = urllib.request.Request(
        endpoint,
        data=body,
        method="POST",
        headers={
            "Authorization": f"Bearer {api_key}",
            "Content-Type": content_type,
            "Accept": "text/event-stream" if stream else "application/json",
            "User-Agent": "sub2api-imagegen/1.1",
        },
    )
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            status = response.status
            content_type = response.headers.get("Content-Type", "").lower()
            if stream and "text/event-stream" in content_type:
                return parse_stream_image_response(response, status, endpoint, api_key)
            raw = response.read()
    except urllib.error.HTTPError as exc:
        body = safe_error_body(exc.read())
        raise SkillError(f"HTTP {exc.code} from {endpoint}: {body}") from exc
    except TimeoutError as exc:
        raise SkillError(f"Request to {endpoint} timed out after {timeout}s") from exc
    except urllib.error.URLError as exc:
        raise SkillError(f"Cannot reach {endpoint}: {exc.reason}") from exc

    return parse_json_image_response(raw, status, endpoint, api_key)


def atomic_write(path: Path, data: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temp_name = ""
    try:
        with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as handle:
            temp_name = handle.name
            handle.write(data)
        os.replace(temp_name, path)
    finally:
        if temp_name and os.path.exists(temp_name):
            os.unlink(temp_name)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    prompt_group = parser.add_mutually_exclusive_group(required=True)
    prompt_group.add_argument("--prompt")
    prompt_group.add_argument("--prompt-file")
    parser.add_argument("--out", required=True)
    parser.add_argument("--base-url")
    parser.add_argument("--model", default=DEFAULT_MODEL)
    parser.add_argument("--image", action="append", default=[], help="Reference image; repeat for multiple images (uses /images/edits)")
    parser.add_argument("--mask", help="Optional PNG mask for edits")
    parser.add_argument("--size")
    parser.add_argument("--aspect-ratio")
    parser.add_argument("--n", type=int, default=1)
    parser.add_argument("--quality", choices=(*QUALITIES, "xhigh", "max", "standard", "hd"))
    parser.add_argument("--output-format", choices=FORMATS)
    parser.add_argument("--output-compression", type=int)
    parser.add_argument("--background", choices=("auto", "opaque", "transparent"))
    parser.add_argument("--moderation", choices=("auto", "low"))
    parser.add_argument("--input-fidelity", choices=("low", "high"))
    parser.add_argument("--partial-images", type=int)
    parser.add_argument("--user")
    parser.add_argument("--style", choices=("vivid", "natural"))
    parser.add_argument("--response-format", choices=("b64_json", "url"))
    parser.add_argument("--timeout", type=int, default=300)
    parser.add_argument(
        "--no-stream",
        action="store_true",
        help="Use the legacy non-streaming JSON response instead of SSE",
    )
    parser.add_argument("--exact-size", action="store_true")
    parser.add_argument("--force", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    return parser


def main() -> int:
    args = build_parser().parse_args()
    try:
        if args.timeout <= 0:
            raise SkillError("--timeout must be a positive integer")
        prompt = prompt_from_args(args)
        editing = bool(args.image)
        validate_parameters(args, editing)
        size = selected_size(args.size, args.aspect_ratio, args.model)
        requested_dimensions = parse_size(size, args.model)
        if args.exact_size and requested_dimensions is None:
            raise SkillError("--exact-size requires an explicit WIDTHxHEIGHT size")

        output = Path(args.out).expanduser().resolve()
        output_format = args.output_format or "png"
        expected_suffix = ".jpg" if output_format == "jpeg" else f".{output_format}"
        if output.suffix.lower() not in {expected_suffix, ".jpeg" if output_format == "jpeg" else expected_suffix}:
            raise SkillError(
                f"Output extension {output.suffix or '<none>'} does not match {output_format}"
            )
        outputs = [output] if args.n == 1 else [output.with_name(f"{output.stem}-{index}{output.suffix}") for index in range(1, args.n + 1)]
        if not args.force and any(path.exists() for path in outputs):
            raise SkillError("An output already exists; use --force only for an approved replacement")

        endpoint, base_url_source = resolve_endpoint(args.base_url, editing)
        _, credential_source = resolve_api_key(required=False)
        stream = not args.no_stream
        payload = request_payload(args, prompt, size, editing)
        if args.dry_run:
            print(
                json.dumps(
                    {
                        "endpoint": endpoint,
                        "base_url_source": base_url_source,
                        "credential_source": credential_source,
                        "payload": payload,
                        "reference_images": [Path(path).name for path in args.image],
                        "mask": Path(args.mask).name if args.mask else None,
                        "outputs": [str(path) for path in outputs],
                        "exact_size": args.exact_size,
                        "stream": stream,
                    },
                    ensure_ascii=False,
                    indent=2,
                )
            )
            return 0

        if args.exact_size:
            require_pillow()
        api_key, credential_source = resolve_api_key(required=True)
        assert api_key is not None
        image_data, response = call_image_api(
            endpoint, api_key, payload, args.timeout, stream, args.image, args.mask
        )
        if len(image_data) != args.n:
            raise SkillError(f"Expected {args.n} final images, received {len(image_data)}")
        image_summaries = []
        for path, data in zip(outputs, image_data):
            api_dimensions = image_dimensions(data)
            if api_dimensions is None:
                raise SkillError("Cannot verify returned image dimensions; install Pillow for JPEG/WebP or check the response image")
            if requested_dimensions and api_dimensions:
                requested_ratio = requested_dimensions[0] / requested_dimensions[1]
                actual_ratio = api_dimensions[0] / api_dimensions[1]
                if abs(requested_ratio - actual_ratio) / requested_ratio > 0.025:
                    raise SkillError(f"API image aspect ratio shifted: {api_dimensions[0]}x{api_dimensions[1]}")
            resized = False
            if args.exact_size and requested_dimensions and api_dimensions != requested_dimensions:
                data = resize_exact(data, requested_dimensions, output_format)
                resized = True
            final_dimensions = image_dimensions(data)
            image_summaries.append((path, data, api_dimensions, final_dimensions, resized))
        for path, data, _, _, _ in image_summaries:
            atomic_write(path, data)

        summary: dict[str, Any] = {
            "status": 200,
            "endpoint": endpoint,
            "base_url_source": base_url_source,
            "credential_source": credential_source,
            "model": args.model,
            "stream": stream,
            "requested_size": size,
            "output_format": output_format,
            "images": [
                {"output": str(path), "api_image_size": "x".join(map(str, original)) if original else "unknown",
                 "final_image_size": "x".join(map(str, final)) if final else "unknown",
                 "resized": resized, "bytes": len(data)}
                for path, data, original, final, resized in image_summaries
            ],
        }
        if isinstance(response.get("usage"), dict):
            summary["usage"] = response["usage"]
        if isinstance(response.get("partial_images_received"), int):
            summary["partial_images_received"] = response[
                "partial_images_received"
            ]
        print(json.dumps(summary, ensure_ascii=False, indent=2))
        return 0
    except (SkillError, OSError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
