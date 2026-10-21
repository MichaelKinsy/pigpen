// Generates the data tables of temml 0.13.4 (src/symbols.js, unicodeSymbols.js, unicodeAccents.js, asciiFromScript.js,
// unicodeSupOrSub.js, functions/color.js, the string macros, and with the second argument the macros and the mhchem
// tables of dist/temml.cjs) into extensions/websearch/tables/tm-*.gz (gen-tables-util.mjs). The sources use
// extensionless imports, so a resolve hook adds ".js". Usage (cwd components/websearch):
//   node port/gen-defuddle-temml-tables.mjs <path to temml/src> [<path to temml/dist/temml.cjs>]
import { register } from 'node:module'
import path from 'node:path'
import { writeTable } from './gen-tables-util.mjs'
const src = path.resolve(process.argv[2])
register('data:text/javascript,' + encodeURIComponent(`
export async function resolve(specifier, context, next) {
  try { return await next(specifier, context) } catch (e) {
    if (specifier.startsWith('.') && !specifier.endsWith('.js')) return next(specifier + '.js', context)
    throw e
  }
}`), import.meta.url)
const symbolsMod = await import(path.join(src, 'symbols.js'))
const uniSym = (await import(path.join(src, 'unicodeSymbols.js'))).default
const uniAcc = (await import(path.join(src, 'unicodeAccents.js'))).default
const { asciiFromScript } = await import(path.join(src, 'asciiFromScript.js'))
const { uSubsAndSups } = await import(path.join(src, 'unicodeSupOrSub.js'))
await import(path.join(src, 'functions.js'))
const { _macros } = await import(path.join(src, 'defineMacro.js'))
const outDir = path.resolve('extensions/websearch/tables')
const table = (name, rows) => writeTable(outDir, name, rows)
for (const mode of ['math', 'text']) {
  // entries that share an object (acceptUnicodeChar) are written as separate but equal rows: nothing mutates them;
  // "replace" can be undefined, which is the empty string here
  table(`tm-symbols-${mode}`, Object.entries(symbolsMod.default[mode]).map(([k, v]) => [k, v.group, v.replace == null ? '' : v.replace]))
}
table('tm-ligatures', Object.keys(symbolsMod.ligatures).map((k) => [k]))
table('tm-unicode-symbols', Object.entries(uniSym))
table('tm-unicode-accents', Object.entries(uniAcc).map(([k, v]) => [k, v.text || '', v.math || '']))
table('tm-ascii-from-script', Object.entries(asciiFromScript))
table('tm-subs-and-sups', Object.entries(uSubsAndSups))
{
  // the xcolor names of src/functions/color.js, a JSON.parse of a template literal
  const colorSrc = (await import('node:fs')).readFileSync(path.join(src, 'functions/color.js'), 'utf8')
  const m = /JSON\.parse\(`([^`]*)`\)/.exec(colorSrc)
  table('tm-xcolors', Object.entries(JSON.parse(m[1])))
}
// Macros defined as a string (the macros defined by a function are in dfd_tm_macros.go)
table('tm-string-macros', Object.entries(_macros).filter(([, v]) => typeof v === 'string'))
console.log('macros defined by a function: ' + Object.entries(_macros).filter(([, v]) => typeof v !== 'string').map(([k]) => k).join(' '))
// The macros the bundled dist/temml.cjs adds to src/: mhchem's dash macros, texvc.sty and the physics package. Their
// source is evaluated with a stub defineMacro.
if (process.argv[3]) {
  const dist = (await import('node:fs')).readFileSync(process.argv[3], 'utf8')
  const found = {}
  const run = (code) => new Function('defineMacro', code)((name, body) => { if (typeof body === 'string') found[name] = body })
  const mh = dist.slice(dist.indexOf('// Add \\ce, \\pu, and \\tripleDash'), dist.indexOf('  //\n  //  This is the main function for handing the \\ce'))
  run(mh.replace(/defineMacro\("\\\\ce"[\s\S]*?\n\}\);\n\ndefineMacro\("\\\\pu"[\s\S]*?\n\}\);/, ''))
  const tx = dist.slice(dist.indexOf('// texvc.sty'), dist.indexOf('// Helper functions'))
  run(tx)
  // mhchem: the state machine tables (as createTransitions builds them) and the regular expression patterns. A row of
  // tm-mh-transitions: machine, state, pattern, next state, revisit, toContinue, then the actions as (type, option) pairs.
  const mhCode = dist.slice(dist.indexOf('// Add \\ce, \\pu, and \\tripleDash'), dist.indexOf('// texvc.sty'))
  const mhc = new Function('defineMacro', mhCode + '\nreturn { mhchemParser }')(() => {})
  const rows = []
  for (const [mname, mach] of Object.entries(mhc.mhchemParser.stateMachines)) {
    for (const [state, list] of Object.entries(mach.transitions)) {
      for (const t of list) {
        rows.push([mname, state, t.pattern, t.task.nextState || '', t.task.revisit ? 1 : 0, t.task.toContinue ? 1 : 0,
          ...t.task.action_.flatMap((a) => [a.type_, a.option === undefined ? '' : String(a.option)])])
      }
    }
  }
  table('tm-mh-transitions', rows)
  table('tm-mh-patterns', Object.entries(mhc.mhchemParser.patterns.patterns).filter(([, pat]) => typeof pat !== 'function').map(([name, pat]) => [name, pat.source]))
  table('tm-dist-macros', Object.entries(found))
}
