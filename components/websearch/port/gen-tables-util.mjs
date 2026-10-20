// Shared by the table generators (gen-defuddle-temml-tables.mjs, gen-defuddle-mathml-tables.mjs): a table is a list of
// rows of strings, written as records separated by U+0001 with the fields separated by U+0000, gzipped, to
// extensions/websearch/tables/<name>.gz. The package embeds the files and decodes one on first use (dfd_tables.go), so
// the tables cost neither start-up time nor the code a map literal compiles to.
import { mkdirSync, writeFileSync } from 'node:fs'
import { gzipSync } from 'node:zlib'

export function writeTable(dir, name, rows) {
  const records = rows.map((row) => {
    const fields = row.map(String)
    for (const f of fields) if (/[\u0000\u0001]/.test(f)) throw new Error(`${name}: a separator occurs in ${JSON.stringify(f)}`)
    return fields.join('\u0000')
  })
  mkdirSync(dir, { recursive: true })
  writeFileSync(`${dir}/${name}.gz`, gzipSync(Buffer.from(records.join('\u0001'), 'utf8'), { level: 9 }))
  console.log(`${name}: ${rows.length} rows`)
}
