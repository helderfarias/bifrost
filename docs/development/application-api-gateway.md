# Application API gateway

The **APIs** sidebar section manages HTTP application routes alongside Models.
Routes are persisted in the existing config store, under
`application_api_gateway_routes`. Dashboard authentication must be enabled before
editing routes. The management API is `GET`/`PUT /api/api-gateway/routes`; PUT takes
`{ "revision": "<revision returned by GET>", "routes": [...] }` and returns 409 for
stale edits. A save replaces the route list atomically within this process.

## OCR example

Create an API with public path `/api/v2/ocr`, upstream `http://localhost:3000`,
**Strip public path prefix**, **Relay Authorization header**, and required
capabilities `svc.ocr,svc.all`. Add a consumer with a unique random access key of
at least 32 characters and capability `svc.ocr`. Keep the original key securely:
only its SHA-256 digest is stored. A consumer must possess at least one of the
route's required capabilities; `svc.all` is an explicit capability, not a wildcard.

`POST /api/v2/ocr/jobs?format=json` forwards to
`POST http://localhost:3000/jobs?format=json`, preserving the request body. The base
path `/api/v2/ocr` maps to `/`. Consumers authenticate with `X-Bifrost-API-Key`.
Their `Authorization` header is relayed only when configured; this is header
forwarding, not OAuth token refresh/exchange. Gateway keys, dashboard cookies and
caller-supplied capability headers are never forwarded upstream.

Paths must live under `/api/v2/` or `/gateway/` to protect management, dashboard and
inference endpoints. Matching is case sensitive and includes exact paths and
subpaths at a slash boundary. The longest enabled prefix wins. Configure another
route for a different upstream or access policy. Removing a consumer revokes its
key for subsequent requests; removing/disabling a route stops new traffic.

## Limits and availability

The request limit is a fixed UTC minute window **per route and consumer**. Zero
means unlimited. Rejected requests return 429 and Retry-After; rejected credentials
return 401, missing capabilities 403. Quota and circuit state are local to one
process and reset on restart; they are not distributed Bucket4j or Redis quotas.
Edits preserve state for retained route IDs and consumer hashes.

Transport errors and upstream 5xx responses count toward the circuit's consecutive
failure threshold. While open, it returns 503 without contacting the upstream.
After the recovery period, one request probes the upstream. Success closes the
circuit; failure opens it again. Transport failures return a generic 502 without
exposing internal network details. The full request has a configured timeout;
responses are buffered and limited to 32 MiB. Streaming, SSE, WebSockets and gRPC
are not supported by this application proxy. LLM inference paths are unaffected.

Private HTTP(S) upstreams, including localhost services, are intentionally supported
for authenticated operators. URL credentials and metadata/link-local/unspecified
addresses are rejected, including after DNS resolution. Redirect responses are
returned to callers rather than followed by the gateway.

This first version targets one Bifrost instance. Other replicas do not hot-reload
route edits; use coordinated configuration/restarts or implement distributed
configuration and quotas before multi-replica deployment. Enterprise installations
must map `/api/api-gateway/routes` to their administrative routing permissions;
the UI uses Routing Rules permissions. Billing integration and Spring's forwarded
fallback controller are deferred. Open circuits return 503.

## Validation

From the checkout root with the local Go workspace active:

```bash
cd transports/bifrost-http
go test -race ./apigateway ./handlers -run 'Test(OCRProxy|AccessQuotaAndMatching|CircuitAndRecovery|LongestPrefixAndRelayDisabled|Validation|ConcurrentReplacement|UpstreamFailure|APIGateway)'
```

Build the UI with `cd ui && npm run build`. The Playwright spec
`tests/e2e/features/apis/apis.spec.ts` covers UI create/edit/delete, a real local OCR
upstream, missing-key rejection and disabled routes. It requires a running Bifrost
with dashboard authentication, plus the existing E2E admin credentials configured
through environment variables. No live LLM provider is needed.
