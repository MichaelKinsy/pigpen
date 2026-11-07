// The catalogue files (the generated index, the schemas for it and for a listing, and the repository config) live in
// catalog/ at the root of the repository. Every script that reads or writes one asks for its path here.
import { join } from 'node:path';

export const CATALOG_DIR = 'catalog';

/** The path of `name` (index.json, index.schema.json, listing.schema.json or catalog.config.json) under `directory`. */
export const catalogPath = (directory, name) => join(directory, CATALOG_DIR, name);
