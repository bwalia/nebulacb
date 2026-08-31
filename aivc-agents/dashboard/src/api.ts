import type { Persona } from "./personas";

export type ApiResult = {
  ok: boolean;
  status: number;
  latencyMs: number;
  body: unknown;
  error?: string;
};

export function headersFor(persona: Persona): HeadersInit {
  return {
    "Content-Type": "application/json",
    "X-User": persona.user,
    "X-Roles": persona.roles,
    "X-Scopes": persona.scopes,
    "X-Tenant": persona.tenant,
  };
}

export async function apiCall(
  method: string,
  path: string,
  persona: Persona | null,
  body?: unknown,
): Promise<ApiResult> {
  const started = performance.now();
  try {
    const init: RequestInit = {
      method,
      headers: persona ? headersFor(persona) : { "Content-Type": "application/json" },
    };
    if (body !== undefined) init.body = JSON.stringify(body);
    const res = await fetch(path, init);
    const text = await res.text();
    let parsed: unknown = text;
    try {
      parsed = text ? JSON.parse(text) : null;
    } catch {
      /* keep text */
    }
    return {
      ok: res.ok,
      status: res.status,
      latencyMs: Math.round(performance.now() - started),
      body: parsed,
    };
  } catch (err) {
    return {
      ok: false,
      status: 0,
      latencyMs: Math.round(performance.now() - started),
      body: null,
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

export function pickRunId(body: unknown): string | null {
  if (!body || typeof body !== "object") return null;
  const o = body as Record<string, unknown>;
  if (typeof o.run_id === "string") return o.run_id;
  const queue = o.awaiting_approval;
  if (Array.isArray(queue) && queue[0] && typeof queue[0] === "object") {
    const first = queue[0] as Record<string, unknown>;
    if (typeof first.run_id === "string") return first.run_id;
  }
  return null;
}
