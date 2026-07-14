export interface SupporterLedgerDetail {
  [key: string]: unknown;
}

export interface SupporterLedgerEntry {
  id: string;
  kind: string;
  source: string;
  action: string;
  exp: number;
  previous_exp: number;
  new_exp: number;
  created: string;
  arcade_id?: string;
  arcade_name?: string;
  target_id?: string;
  target_name?: string;
  detail?: SupporterLedgerDetail;
}

export interface SupporterLatestRequest {
  id: string;
  status: string;
  exp_total: number;
  qualified: boolean;
  created: string;
  decision_reason?: string;
}

export interface SupporterScoreResponse {
  total_exp: number;
  attendance_exp: number;
  qualified: boolean;
  threshold: number;
  can_request: boolean;
  entries: SupporterLedgerEntry[];
  latest_request?: SupporterLatestRequest | null;
}

export interface SupporterRequestResponse {
  id: string;
  status: string;
  qualified: boolean;
  exp: SupporterScoreResponse;
}

export interface SupporterApiError {
  error: string;
  details?: string;
}

export function isSupporterApiError(error: unknown): error is SupporterApiError {
  if (!error || typeof error !== "object") return false;
  const e = error as Record<string, unknown>;
  return typeof e.error === "string";
}

export async function fetchSupporterScore(baseUrl: string, token: string): Promise<SupporterScoreResponse> {
  const url = new URL("/supporter/score", baseUrl);
  const res = await fetch(url.toString(), {
    method: "GET",
    headers: {
      Authorization: `Bearer ${token}`,
    },
  });

  if (!res.ok) {
    throw (await res.json()) as SupporterApiError;
  }

  return (await res.json()) as SupporterScoreResponse;
}

export async function requestSupporterVerification(baseUrl: string, token: string): Promise<SupporterRequestResponse> {
  const url = new URL("/supporter/request", baseUrl);
  const res = await fetch(url.toString(), {
    method: "POST",
    headers: {
      Authorization: `Bearer ${token}`,
    },
  });

  if (!res.ok) {
    throw (await res.json()) as SupporterApiError;
  }

  return (await res.json()) as SupporterRequestResponse;
}
