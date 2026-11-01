// HTTP helpers: JSON responses, the error type, and the headers that every response gets.

export class HttpError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly extra: Record<string, unknown> = {},
    readonly headers: Record<string, string> = {},
  ) {
    super(message);
  }
}

export function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: {
      "content-type": "application/json; charset=utf-8",
      "cache-control": "no-store",
      ...headers,
    },
  });
}

export function errorResponse(err: HttpError): Response {
  return json(err.status, { error: err.code, message: err.message, ...err.extra }, err.headers);
}

// The maximum size of a request body. All bodies of this API are small JSON objects.
export const MAX_BODY_BYTES = 4096;

export async function readBody(request: Request): Promise<Uint8Array> {
  const declared = request.headers.get("content-length");
  if (declared !== null && Number(declared) > MAX_BODY_BYTES) {
    throw new HttpError(413, "payload_too_large", `The request body must be ${MAX_BODY_BYTES} bytes or less.`);
  }
  if (request.body === null) return new Uint8Array(0);
  const reader = request.body.getReader();
  const chunks: Uint8Array[] = [];
  let total = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    total += value.byteLength;
    if (total > MAX_BODY_BYTES) {
      await reader.cancel();
      throw new HttpError(413, "payload_too_large", `The request body must be ${MAX_BODY_BYTES} bytes or less.`);
    }
    chunks.push(value);
  }
  const out = new Uint8Array(total);
  let offset = 0;
  for (const c of chunks) {
    out.set(c, offset);
    offset += c.byteLength;
  }
  return out;
}

const decoder = new TextDecoder("utf-8", { fatal: true, ignoreBOM: false });

// Parses a JSON object body. An empty body gives an empty object.
export function parseJsonObject(body: Uint8Array, errorCode = "bad_request"): Record<string, unknown> {
  if (body.byteLength === 0) return {};
  let value: unknown;
  try {
    value = JSON.parse(decoder.decode(body));
  } catch {
    throw new HttpError(400, errorCode, "The request body must be a JSON object.");
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new HttpError(400, errorCode, "The request body must be a JSON object.");
  }
  return value as Record<string, unknown>;
}

// Headers for every response. HSTS has no includeSubDomains: clinic names are separate hosts on clinic LANs.
const COMMON_HEADERS: Record<string, string> = {
  "x-content-type-options": "nosniff",
  "referrer-policy": "no-referrer",
  "strict-transport-security": "max-age=31536000",
  "cross-origin-opener-policy": "same-origin",
  "cross-origin-resource-policy": "same-origin",
};

export function withCommonHeaders(response: Response): Response {
  const out = new Response(response.body, response);
  for (const [k, v] of Object.entries(COMMON_HEADERS)) {
    if (!out.headers.has(k)) out.headers.set(k, v);
  }
  return out;
}

// The public IP address of the caller. Cloudflare sets CF-Connecting-IP on every request to a Worker.
export function callerIp(request: Request): string {
  return request.headers.get("cf-connecting-ip") ?? "";
}
