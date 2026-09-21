---
name: sub2api-imagegen
description: 通过 Sub2API 图片 HTTP 接口生成或编辑图片，支持参考图与 mask、流式/非流式、多图输出和尺寸校验。适用于调用 /v1/images/generations 或 /v1/images/edits，以及排查生图超时、参数或尺寸偏移。
---

# Sub2API 生图与编辑

使用 `scripts/generate_image.py` 调用图片接口。提供 `--image` 时自动走 `/v1/images/edits`，可重复指定参考图；`--mask` 只用于编辑。默认 GPT Image 2、SSE 流、`quality=high`、PNG、`2048x1152`。DALL-E 模型须使用 `--no-stream`。不要打印 API Key 或完整 Base64 响应。

先用 `--dry-run` 检查脱敏后的 endpoint、实际 `size` 和发送参数，再执行请求。常见用法：

```powershell
python <skill-dir>\scripts\generate_image.py --prompt "保持人物，替换背景" --image input.png --input-fidelity high --aspect-ratio 16:9 --output-format png --out output/imagegen/edit.png --dry-run
python <skill-dir>\scripts\generate_image.py --prompt "保持人物，替换背景" --image input.png --input-fidelity high --aspect-ratio 16:9 --output-format png --out output/imagegen/edit.png
```

`--aspect-ratio` 是本地选尺寸辅助参数，**不会**发给服务端；与显式 `--size` 冲突时拒绝请求。输出原始尺寸及最终尺寸；只有用户明确要求精确像素时才用依赖 Pillow 的 `--exact-size`，比例偏差超过 2.5% 时不裁切伪装成功。`--n` 大于 1 时以 `-1`、`-2` 后缀分别保存，不覆盖已有文件，除非明确使用 `--force`。

根据模型与接口仅发送支持的参数。生成和编辑的可选参数、尺寸与错误判定见 [references/api.md](references/api.md)。编辑专属的 `input_fidelity` 不得用于生成；DALL-E 参数不得发送到 GPT Image。流式需要完成事件，partial image 和 HTTP 200 均不代表成功。

API Key 来源：`OPENAI_API_KEY` → `$CODEX_HOME/auth.json` → `~/.codex/auth.json`；Base URL 来源：`--base-url` → `OPENAI_BASE_URL` → 当前 Codex provider 的 `base_url`。脚本仅读取 `auth.json` 顶层 `OPENAI_API_KEY`，绝不把认证信息放入提示词、文件或日志。
