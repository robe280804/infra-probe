// Accessors over the report JSON and the links between pages.

export const dockerOf = (report) => report?.results?.docker ?? { containers: [], images: [], networks: [], volumes: [] };
export const containersOf = (report) => dockerOf(report).containers ?? [];
export const imagesOf = (report) => dockerOf(report).images ?? [];
export const networksOf = (report) => dockerOf(report).networks ?? [];
export const daemonOf = (report) => dockerOf(report).daemon ?? null;
export const volumesOf = (report) => dockerOf(report).volumes ?? [];

export const containerHref = (c) => `#/containers/${encodeURIComponent(c.id)}`;
export const imageHref = (i) => `#/images/${encodeURIComponent(i.id)}`;
export const networkHref = (n) => `#/networks/${encodeURIComponent(n.id)}`;
export const volumeHref = (v) => `#/volumes/${encodeURIComponent(v.name)}`;
