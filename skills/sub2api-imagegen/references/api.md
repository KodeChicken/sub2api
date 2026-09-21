# 图片接口参数与响应

生成请求使用 JSON `POST /v1/images/generations`；编辑请求使用 multipart `POST /v1/images/edits`，参考图用重复的 `image[]`，可选 PNG `mask`。响应可为 `data[].b64_json` 或 HTTPS `url`。SSE 模式忽略心跳与 `*.partial_image`，收集 `image_generation.completed` 或 `image_edit.completed`，并检查最终图片数量。

GPT Image 通用参数：`model`、`prompt`、`n`、`size`、`quality`、`background`、`output_format`、`output_compression`（仅 JPEG/WebP）、`moderation`、`stream`、`partial_images`（仅流式）、`user`。`input_fidelity`、参考图及 mask 仅用于编辑。`aspect_ratio` 是本地尺寸选择，不发给 API。

GPT Image 2/2.5 自定义尺寸须满足：两边是 16 的倍数，最长边不超过 3840，总像素 655360..8294400，长短边比不超过 3:1。GPT Image 1/1.5 仅用 `1024x1024`、`1536x1024`、`1024x1536` 或 `auto`。`xhigh`、`max` 质量只用于 GPT Image 2.5。DALL-E 2/3 采用各自固定尺寸；DALL-E 3 的 `style` 与 `quality=standard|hd` 不用于其他模型，DALL-E 不使用 GPT Image 的流式及格式参数。

默认流式可以减少反向代理的空闲超时，但不能缩短上游执行时间。服务端需持续转发 SSE 心跳并关闭代理缓冲；Cloudflare 524 若发生在响应头之前，仍需排查源站。对 400/403 不自动重试；对 429/5xx 只在用户授权且请求可安全重试时最多重试一次。

官方参数与模型能力以 OpenAI Images API 的 Generate/Edit 文档为准；Sub2API 自定义模型别名仍须由服务端能力确认。返回尺寸与请求尺寸不一致时如实报告，不暗中改写。
