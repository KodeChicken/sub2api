# Native Kiro Integration

The `kiro` provider implements the Kiro protocol inside Sub2API. It does not
launch, embed, or forward requests through a kiro.rs proxy server.

Protocol reference: `zyphrzero/kiro.rs`, version 0.9.4, commit
`e768cef18b6aa4e614845712987f7cc087d49a0e`.
The original MIT attribution is in `backend/resources/kiro.rs.NOTICE`, included
in release archives and container runtime resources.

## Request Path

```text
Messages / Responses / Chat Completions
  -> existing authentication, group routing, account scheduling
  -> Kiro-only adapter
  -> conversationState + native IDE/CLI endpoint
  -> AWS EventStream decoding
  -> client protocol + existing Sub2API usage accounting
```

Only accounts with `platform=kiro` enter the adapter. Kiro credentials are
explicitly excluded from the generic Anthropic transport and billing probe.
Existing providers retain their forwarding implementations.

The adapter supports text, base64 images, conversation/tool-result history,
automatic client function tools, native reasoning, streaming and JSON output,
and Kiro MCP web search. Search runs up to five inference rounds and five
search calls; client-side tools are returned to the client, never executed
by the server. Search and compaction responses are buffered before delivery.

Responses remote compaction accepts a unique terminal `compaction_trigger`,
generates a continuation summary, and returns a `compaction` item. Subsequent
requests can replay that item. The `kiro-rs.compaction.v1:` payload is an opaque
protocol envelope containing plain text, **not encryption**. Truncated or
refused summaries are not reported as successful compaction.

## Accounts

Create a Kiro account in the admin account dialog. Import a single-account IDE
or kiro.rs credential JSON file, or enter the fields manually. Both camelCase
exports and Sub2API snake_case fields are accepted.

| Authentication | Required Fields |
| --- | --- |
| GitHub / Google | `refresh_token`, `auth_method=social` |
| Builder ID / AWS IdC | `refresh_token`, `client_id`, `client_secret`, `auth_method=idc` |
| Microsoft Entra ID | `refresh_token`, `client_id`, `token_endpoint`, `auth_method=external_idp` |
| Kiro API key | `api_key` starting with `ksk_`, account type `apikey` |

Optional fields include `auth_region`, `api_region`, `profile_arn`, `machine_id`,
`endpoint` (`ide` or `cli`), and `api_domain` (`q` or `runtime`).
Entra token endpoints are restricted to HTTPS tenant endpoints under
`login.microsoftonline.com`. Browser sign-in/device authorization is not hosted
by this integration; import credentials obtained with your own authorized account.

OAuth refresh reuses Sub2API's local/distributed locking. Credential updates
compare the complete original credential document and proxy, and atomically
publish scheduler invalidation. A concurrent admin reauthorization cannot be
overwritten by a stale refresh result. Blank secrets on edit preserve stored
secrets. Replacing only a refresh token invalidates the old access token.

Enterprise profiles are discovered when needed; discovery results are cached
for one hour and tied to the credential document. Models and usage queries use
the same profile discovery and configured account proxy.

## Models And Billing

After creation, edit the account and synchronize the model whitelist. The
native `ListAvailableModels` result is authoritative for available model IDs.
No Kiro model list or account entitlement is invented from Claude defaults.
Imported/API-supplied `model_mapping` aliases remain supported.

Bind the account to a Kiro group, or an explicitly configured composite group,
and use the existing Sub2API key and public endpoints. No second API key or
proxy URL is required on the client.

The native quota query displays monthly credits, active trials and unexpired
bonuses. Unknown quota is not interpreted as zero usage.

The last `metadataEvent.tokenUsage` snapshot supplies exact uncached input,
output, cache-read and cache-write tokens. Calls without that snapshot use
local estimates. Multi-round search adds usage across provider calls.
`kiro_last_usage_source` records `provider` or `estimate`; credit consumption is
stored separately as `kiro_last_request_credits`. Credit units are not dollars,
and no automatic credit-to-dollar exchange rate is inferred. Configure normal
Sub2API model pricing/rate multipliers before exposing paid access.

## Explicit Limits

- HTTP JSON/SSE only; no Kiro WebSocket or realtime transport.
- Full history is required; `previous_response_id` storage is not supported.
- Assistant prefill and remote image URLs are rejected.
- Only automatic tool selection is supported. Forced/disabled `tool_choice`
  and schema-enforced structured output return a clear 400 response.
- Kiro has no native sampling/output-token fields in this envelope. Client
  `max_tokens`, sampling and stop-sequence controls are not strict upstream
  guarantees. Use a native provider when those controls are required.
- Compaction does not automatically truncate history or retry oversized tool
  outputs. Context overflow is surfaced to the client.
- Native model metadata includes reported token limits and descriptions.
  Missing reasoning/modality metadata remains incomplete, with the existing
  admin warning, rather than guessed capabilities.
- Kiro uses upstream-specific APIs; protocol changes or account permissions
  can break compatibility. This does not grant additional upstream entitlements.

## Verification

The added tests cover all three client protocols in JSON/SSE modes, EventStream
length/CRC validation, tools, reasoning, images, credits, search, compaction,
refresh races, credential isolation, repository CAS predicates/outbox, and
admin form behavior. Run the existing backend unit and frontend suites too.

Live inference/refresh/quota validation requires an authorized Kiro account.
No real Kiro credentials are included in the repository or test fixtures.

The full backend unit run observed two existing failures, both reproduced at
the unmodified baseline commit `b2d1edca4b09b82cddc3263378c66bd84820651b`:

- `TestCompatibleImagesForwardGemini/account_mapping`: the compatible image
  path drops `custom_field` after account model mapping.
- `TestOllamaProbeCallback_StaleLongDoesNotOverrideNewShort`: the stale callback
  passes the test's CAS assertion.

Those image and Ollama implementations were deliberately left unchanged.
