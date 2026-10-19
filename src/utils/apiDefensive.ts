export interface ApiResponse<T = any> {
  success: boolean;
  data?: T;
  error?: string;
  status: number;
}

/**
 * Defensively fetches and parses JSON responses from Air Traffic Control API endpoints.
 * Guards against Vite SPA HTML fallthrough (`<!doctype html>`), network errors,
 * non-JSON content types, and server-side errors, preventing unhandled JSON SyntaxErrors.
 */
export async function safeFetchJson<T = any>(
  url: string,
  options?: RequestInit
): Promise<ApiResponse<T>> {
  let res: Response;
  try {
    res = await fetch(url, options);
  } catch (err: any) {
    return {
      success: false,
      error: err?.message || 'Network error communicating with server',
      status: 0,
    };
  }

  // Handle 204 No Content
  if (res.status === 204) {
    return {
      success: true,
      data: undefined as any,
      status: 204,
    };
  }

  const contentType = (res.headers.get('content-type') || '').toLowerCase();

  // If content-type explicitly indicates HTML
  if (contentType.includes('text/html')) {
    const statusInfo = `${res.status}${res.statusText ? ` ${res.statusText}` : ''}`;
    return {
      success: false,
      error: `Server returned HTML (${statusInfo}) instead of JSON`,
      status: res.status,
    };
  }

  let text: string;
  try {
    text = await res.text();
  } catch (err: any) {
    return {
      success: false,
      error: err?.message || 'Failed to read response body',
      status: res.status,
    };
  }

  // Detect HTML body even if content-type header was omitted or generic
  const trimmed = text.trim();
  const lowerTrimmed = trimmed.toLowerCase();
  if (
    lowerTrimmed.startsWith('<!doctype html') ||
    lowerTrimmed.startsWith('<html') ||
    lowerTrimmed.startsWith('<!doctype')
  ) {
    const statusInfo = `${res.status}${res.statusText ? ` ${res.statusText}` : ''}`;
    return {
      success: false,
      error: `Server returned HTML (${statusInfo}) instead of JSON`,
      status: res.status,
    };
  }

  if (!trimmed) {
    return {
      success: res.ok,
      data: undefined,
      error: res.ok ? undefined : `HTTP error ${res.status}`,
      status: res.status,
    };
  }

  let parsed: any;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return {
      success: false,
      error: `Invalid JSON response from server (HTTP ${res.status})`,
      status: res.status,
    };
  }

  // If server returned non-2xx status code
  if (!res.ok) {
    const extractedError =
      (typeof parsed === 'object' && parsed !== null && (parsed.error || parsed.message)) ||
      `HTTP error ${res.status}${res.statusText ? `: ${res.statusText}` : ''}`;
    return {
      success: false,
      data: parsed,
      error: String(extractedError),
      status: res.status,
    };
  }

  // If parsed object explicitly has { success: false, ... }
  if (typeof parsed === 'object' && parsed !== null && parsed.success === false) {
    return {
      success: false,
      data: parsed,
      error: parsed.error || parsed.message || 'Request returned failure status',
      status: res.status,
    };
  }

  return {
    success: true,
    data: parsed,
    status: res.status,
  };
}
