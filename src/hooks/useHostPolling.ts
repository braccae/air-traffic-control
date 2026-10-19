import { useEffect, useRef } from 'react';

/**
 * Conservative telemetry polling cadence: 45 seconds (within the 30–60s range).
 * Adheres to conservative homelab & Ansible telemetry polling guidelines.
 */
export const DEFAULT_TELEMETRY_POLL_INTERVAL_MS = 45000;
export const DEFAULT_POLLING_INTERVAL_MS = DEFAULT_TELEMETRY_POLL_INTERVAL_MS;

export interface UseHostPollingOptions {
  intervalMs?: number;
  enabled?: boolean;
  immediate?: boolean;
}

/**
 * Custom React hook for polling host telemetry at a conservative interval.
 */
export function useHostPolling(
  pollFn: () => void | Promise<void>,
  options: UseHostPollingOptions = {}
): void {
  const {
    intervalMs = DEFAULT_TELEMETRY_POLL_INTERVAL_MS,
    enabled = true,
    immediate = false,
  } = options;

  const savedPollFn = useRef(pollFn);

  useEffect(() => {
    savedPollFn.current = pollFn;
  }, [pollFn]);

  useEffect(() => {
    if (!enabled) return;

    if (immediate) {
      void savedPollFn.current();
    }

    const timer = setInterval(() => {
      void savedPollFn.current();
    }, intervalMs);

    return () => clearInterval(timer);
  }, [intervalMs, enabled, immediate]);
}
