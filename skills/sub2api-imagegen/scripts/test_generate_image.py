import argparse
import base64
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import generate_image as imagegen


class ImagegenTest(unittest.TestCase):
    def args(self, **overrides):
        values = dict(model="gpt-image-2.5-flare", image=[], mask=None, n=1,
                      input_fidelity=None, no_stream=False, background=None,
                      output_format=None, output_compression=None, moderation=None,
                      partial_images=None, style=None, response_format=None,
                      quality=None, user=None)
        values.update(overrides)
        return argparse.Namespace(**values)

    def test_generation_and_edit_parameters(self):
        args = self.args(input_fidelity="high")
        with self.assertRaises(imagegen.SkillError):
            imagegen.validate_parameters(args, False)
        with tempfile.TemporaryDirectory() as directory:
            image = Path(directory) / "input.png"
            image.write_bytes(b"\x89PNG\r\n\x1a\n")
            args.image = [str(image)]
            imagegen.validate_parameters(args, True)
            self.assertEqual(imagegen.endpoint_from_base_url("https://example.com/v1", True), "https://example.com/v1/images/edits")
            payload = imagegen.request_payload(args, "edit", "2048x1152", True)
            self.assertEqual(payload["input_fidelity"], "high")
            body, content_type = imagegen.multipart_body(payload, args.image, None)
            self.assertIn(b'name="image[]"', body)
            self.assertIn(b"input_fidelity", body)
            self.assertTrue(content_type.startswith("multipart/form-data; boundary="))

    def test_ratio_and_model_constraints(self):
        self.assertEqual(imagegen.selected_size(None, "16:9", "gpt-image-2.5-flare"), "2048x1152")
        with self.assertRaises(imagegen.SkillError):
            imagegen.selected_size("2048x2048", "16:9", "gpt-image-2")
        with self.assertRaises(imagegen.SkillError):
            imagegen.validate_parameters(self.args(model="gpt-image-2", quality="max"), False)

    def test_stream_waits_for_final_images(self):
        encoded = base64.b64encode(b"image").decode()
        lines = [b": heartbeat\n", b"\n", b'data: {"type":"image_edit.partial_image"}\n', b"\n",
                 f'data: {{"type":"image_edit.completed","b64_json":"{encoded}"}}\n'.encode(), b"\n",
                 f'data: {{"type":"image_edit.completed","b64_json":"{encoded}"}}\n'.encode(), b"\n"]
        images, event = imagegen.parse_stream_image_response(lines, 200)
        self.assertEqual(images, [b"image", b"image"])
        self.assertEqual(event["partial_images_received"], 1)
        with self.assertRaises(imagegen.SkillError):
            imagegen.parse_stream_image_response([b": heartbeat\n", b"\n"], 200)

    def test_relative_result_url_uses_same_origin_auth(self):
        class Response:
            def __enter__(self): return self
            def __exit__(self, *_): return None
            def read(self, _): return b"image"

        with patch.object(imagegen.urllib.request, "urlopen", return_value=Response()) as open_url:
            result = imagegen.image_from_response(
                {"url": "/v1/images/storage/images/result.png"}, "data[0]",
                "https://example.com/v1/images/edits", "secret",
            )
        self.assertEqual(result, b"image")
        request = open_url.call_args.args[0]
        self.assertEqual(request.full_url, "https://example.com/v1/images/storage/images/result.png")
        self.assertEqual(request.get_header("Authorization"), "Bearer secret")


if __name__ == "__main__":
    unittest.main()
