// What the zip export can contain. The keys are the categories understood by
// GET /api/export?include=... (see export.Categories in the Go code).

export const EXPORT_CATEGORIES = [
  { key: "host", label: "Host", hint: "Host name, operating system, CPUs" },
  { key: "daemon", label: "Docker daemon", hint: "Version, drivers, registries, daemon.json, disk usage" },
  { key: "compose", label: "Compose projects", hint: "Projects, services and the sanitized compose files" },
  { key: "containers", label: "Containers", hint: "Full configuration, mounts, ports, live statistics" },
  { key: "images", label: "Images", hint: "Tags, sizes, layers, configuration" },
  { key: "networks", label: "Networks", hint: "Subnets and attached containers" },
  { key: "volumes", label: "Volumes", hint: "Size, usage, driver options" },
];

/**
 * Download URL for a selection: the plain endpoint when everything is chosen,
 * null when nothing is (there is nothing to download).
 */
export function exportUrl(selected) {
  if (selected.length === 0) return null;
  if (selected.length === EXPORT_CATEGORIES.length) return "/api/export";
  // Keep the canonical order, whatever order the boxes were ticked in.
  const keys = EXPORT_CATEGORIES.map((c) => c.key).filter((k) => selected.includes(k));
  return `/api/export?include=${keys.join(",")}`;
}
