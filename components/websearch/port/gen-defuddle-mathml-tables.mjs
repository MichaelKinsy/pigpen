// Generates the lookup tables of mathml-to-latex 1.8.0 (dist/index.cjs) into extensions/websearch/tables/ml-*.gz
// (gen-tables-util.mjs). Usage (cwd components/websearch): node port/gen-defuddle-mathml-tables.mjs <path to index.cjs>
import fs from 'node:fs'
import path from 'node:path'
import { writeTable } from './gen-tables-util.mjs'
const src = fs.readFileSync(process.argv[2], 'utf8')
const outDir = path.resolve('extensions/websearch/tables')
function table(name) {
  const start = src.indexOf(`const ${name} = {`)
  if (start < 0) throw new Error(name)
  const bodyStart = src.indexOf('{', start)
  // the table ends at the first line that is exactly "};"
  const end = src.indexOf('\n};', bodyStart)
  return new Function('return ' + src.slice(bodyStart, end + 2))()
}
for (const [name, file] of [
  ['allMathOperatorsByChar', 'ml-operators-by-char'], ['allMathOperatorsByGlyph', 'ml-operators-by-glyph'],
  ['allMathSymbolsByChar', 'ml-symbols-by-char'], ['allMathSymbolsByGlyph', 'ml-symbols-by-glyph'],
  ['mathNumberByGlyph', 'ml-number-by-glyph'], ['accentToLTXCmd', 'ml-accent-to-cmd'],
]) {
  writeTable(outDir, file, Object.entries(table(name)))
}
writeTable(outDir, 'ml-vowels-with-accents', Object.entries(table('vowelsWithAccents')).map(([k, v]) => [k, v.char, v.accent]))
writeTable(outDir, 'ml-special-font-chars', Object.entries(table('specialFontChars')).map(([k, v]) => [k, v.letter, v.fontCmd]))
