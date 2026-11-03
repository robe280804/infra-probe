// Value formatting helpers.

export function fmtBytes(n) {
  if (typeof n !== "number" || !isFinite(n)) return "—";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(i === 0 || n >= 100 ? 0 : 1)} ${units[i]}`;
}

export function fmtPct(n, digits = 1) {
  return typeof n === "number" && isFinite(n) ? `${n.toFixed(digits)}%` : "—";
}

/** Formats an ISO date; Docker's zero date ("0001-01-01...") means "never". */
export function fmtDate(iso) {
  const d = new Date(iso);
  if (isNaN(d) || d.getFullYear() < 1971) return "—";
  return d.toLocaleString();
}

export function fmtTime(ms) {
  return new Date(ms).toLocaleTimeString();
}

/** Nanoseconds to a short duration string. */
export function fmtNanos(ns) {
  if (!ns) return "—";
  const s = ns / 1e9;
  return s >= 1 ? `${s}s` : `${ns / 1e6}ms`;
}

export function fmtList(arr) {
  return Array.isArray(arr) && arr.length ? arr.join(", ") : "—";
}

export function shortId(id) {
  return String(id || "").replace("sha256:", "").slice(0, 12);
}
