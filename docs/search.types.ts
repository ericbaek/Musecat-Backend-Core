export interface SearchUserItem {
  username: string;
  nickname: string;
  avatar: string; // filename only
  level: number;
}

export interface SearchArcadeItem {
  id: string;
  country: string;
  name: string;
  address: string;
  nickname: string[];
  closed: boolean;
  distance_km?: number;
}

export interface SearchResponse {
  users: SearchUserItem[];
  arcades: SearchArcadeItem[];
}

export interface SearchLocation {
  lat: number;
  lon: number;
}

export interface SearchApiError {
  error: string;
  details?: string;
}

export function isSearchApiError(error: unknown): error is SearchApiError {
  if (!error || typeof error !== "object") return false;
  const e = error as Record<string, unknown>;
  return typeof e.error === "string";
}

export async function searchAll(
  baseUrl: string,
  q: string,
  limit?: number,
  location?: SearchLocation,
): Promise<SearchResponse> {
  const url = new URL("/search", baseUrl);
  url.searchParams.set("q", q);

  if (typeof limit === "number") {
    url.searchParams.set("limit", String(limit));
  }
  if (location) {
    url.searchParams.set("lat", String(location.lat));
    url.searchParams.set("lon", String(location.lon));
  }

  const res = await fetch(url.toString(), { method: "GET" });
  if (!res.ok) {
    throw (await res.json()) as SearchApiError;
  }

  return (await res.json()) as SearchResponse;
}
