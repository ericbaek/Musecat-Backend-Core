export interface UserProfileSNSItem {
  type: string;
  link: string;
  name?: string;
}

export interface UserProfileSNS {
  items: UserProfileSNSItem[];
}

export interface UserProfile {
  id: string;
  username: string;
  nickname: string;
  level: number;
  bio: string;
  avatar: string; // filename only
  sns: UserProfileSNS;
  withdrawn: boolean;
  warp?: boolean;
}

export interface UserActivityRange {
  start_date: string;
  end_date: string;
  tz: string;
  days: number;
}

export interface UserActivityTotals {
  total_count: number;
  changelog_count: number;
  flag_count: number;
  legacy_ticket_count: number;
  attendance_count: number;
  max_daily_count: number;
}

export interface UserActivityDay {
  date: string;
  total_count: number;
  level: number;
  changelog_count: number;
  flag_count: number;
  legacy_ticket_count: number;
  attendance_count: number;
}

export interface UserActivity {
  user: UserProfile;
  range: UserActivityRange;
  totals: UserActivityTotals;
  days: UserActivityDay[];
}

export interface UserApiError {
  error: string;
  details?: string;
}

export interface PocketBaseAuthError {
  data: Record<string, unknown>;
  message: string;
  status: number;
}

export type UserProfileError = UserApiError | PocketBaseAuthError;

export function isPocketBaseAuthError(error: unknown): error is PocketBaseAuthError {
  if (!error || typeof error !== "object") return false;
  const e = error as Record<string, unknown>;
  return typeof e.message === "string" && typeof e.status === "number" && typeof e.data === "object";
}

export function isUserApiError(error: unknown): error is UserApiError {
  if (!error || typeof error !== "object") return false;
  const e = error as Record<string, unknown>;
  return typeof e.error === "string";
}

export async function fetchUserProfileById(baseUrl: string, userId: string): Promise<UserProfile> {
  const url = new URL("/user", baseUrl);
  url.searchParams.set("id", userId);

  const res = await fetch(url.toString(), { method: "GET" });
  if (!res.ok) {
    throw (await res.json()) as UserProfileError;
  }

  return (await res.json()) as UserProfile;
}

export async function fetchMyUserProfile(baseUrl: string, token: string): Promise<UserProfile> {
  const url = new URL("/user/me", baseUrl);
  const res = await fetch(url.toString(), {
    method: "GET",
    headers: {
      Authorization: `Bearer ${token}`,
    },
  });

  if (!res.ok) {
    throw (await res.json()) as UserProfileError;
  }

  return (await res.json()) as UserProfile;
}

export async function fetchUserActivity(
  baseUrl: string,
  params: { id?: string; username?: string; tz?: string; days?: number },
): Promise<UserActivity> {
  const url = new URL("/user/activity", baseUrl);

  if (params.id) url.searchParams.set("id", params.id);
  if (params.username) url.searchParams.set("username", params.username);
  if (params.tz) url.searchParams.set("tz", params.tz);
  if (typeof params.days === "number") url.searchParams.set("days", String(params.days));

  const res = await fetch(url.toString(), { method: "GET" });
  if (!res.ok) {
    throw (await res.json()) as UserProfileError;
  }

  return (await res.json()) as UserActivity;
}
