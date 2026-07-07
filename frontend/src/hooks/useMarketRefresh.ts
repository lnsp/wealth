import { useEffect, useRef } from 'react';

/** Returns true if current time is within German exchange hours (Mon-Fri 08:00-22:00 CET). */
function isMarketOpen(): boolean {
  const now = new Date();
  const berlinHour = parseInt(
    new Intl.DateTimeFormat('en', { hour: 'numeric', hour12: false, timeZone: 'Europe/Berlin' }).format(now),
    10
  );
  const berlinDay = new Date(now.toLocaleString('en-US', { timeZone: 'Europe/Berlin' })).getDay();
  if (berlinDay === 0 || berlinDay === 6) return false;
  return berlinHour >= 8 && berlinHour < 22;
}

/**
 * Polls the given callback every `intervalMs` during European market hours.
 * Stops polling outside market hours. Checks market status every 60s.
 */
export function useMarketRefresh(callback: () => void, intervalMs = 60_000) {
  const cbRef = useRef(callback);
  cbRef.current = callback;

  useEffect(() => {
    let timer: ReturnType<typeof setInterval> | null = null;

    function start() {
      if (timer) return;
      timer = setInterval(() => {
        if (isMarketOpen()) {
          cbRef.current();
        } else {
          stop();
        }
      }, intervalMs);
    }

    function stop() {
      if (timer) {
        clearInterval(timer);
        timer = null;
      }
    }

    // Check every 60s if market is open, start/stop polling accordingly
    const checker = setInterval(() => {
      if (isMarketOpen()) {
        start();
      } else {
        stop();
      }
    }, 60_000);

    // Initial check
    if (isMarketOpen()) start();

    return () => {
      clearInterval(checker);
      stop();
    };
  }, [intervalMs]);
}
