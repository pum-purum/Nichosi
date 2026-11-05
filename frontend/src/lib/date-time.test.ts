import { afterEach, describe, expect, it, vi } from 'vitest';
import { browserTimeZoneLabel, formatDateTime } from './date-time';

describe('browser datetime presentation', () => {
  afterEach(() => vi.restoreAllMocks());

  it.each([[-240, 'UTC+4'], [0, 'UTC'], [300, 'UTC-5'], [-345, 'UTC+5:45'], [210, 'UTC-3:30']])('formats browser offset %s', (offset, label) => {
    vi.spyOn(Date.prototype, 'getTimezoneOffset').mockReturnValue(offset);
    expect(browserTimeZoneLabel('2026-11-04T12:25:34Z')).toBe(label);
  });

  it('uses the offset at the event date', () => {
    const offset = vi.spyOn(Date.prototype, 'getTimezoneOffset').mockImplementation(function (this: Date) {
      return this.getUTCMonth() === 6 ? -120 : -60;
    });
    expect(browserTimeZoneLabel('2026-07-04T12:00:00Z')).toBe('UTC+2');
    expect(browserTimeZoneLabel('2026-11-04T12:00:00Z')).toBe('UTC+1');
    expect(offset).toHaveBeenCalledTimes(2);
  });
  it('uses English months, padding, seconds and a 24-hour clock', () => {
    expect(formatDateTime(new Date(2026, 10, 4, 16, 25, 34).toISOString())).toBe('04 Nov 2026, 16:25:34');
    expect(formatDateTime(new Date(2026, 0, 2, 0, 3, 4).toISOString())).toBe('02 Jan 2026, 00:03:04');
  });

  it('converts an explicit offset into browser local time', () => {
    const instant = '2026-11-04T16:25:34+04:00';
    const local = new Date(instant);
    const pad = (value: number) => String(value).padStart(2, '0');
    expect(formatDateTime(instant)).toBe(`${pad(local.getDate())} Nov 2026, ${pad(local.getHours())}:${pad(local.getMinutes())}:${pad(local.getSeconds())}`);
  });

  it('handles missing and invalid timestamps', () => {
    expect(formatDateTime(null)).toBe('Unavailable');
    expect(formatDateTime('invalid')).toBe('invalid');
  });
});
