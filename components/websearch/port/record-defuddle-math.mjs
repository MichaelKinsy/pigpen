// Records what the original does for the Defuddle math helpers, under Node with the oracle's node_modules
// (mathml-to-latex 1.8.0, temml 0.13.4): the Go port is compared with these files byte for byte.
//   node port/record-defuddle-math.mjs        (cwd components/websearch, after node port/build-defuddle-math-inputs.mjs)
// Inputs:  extensions/websearch/testdata/defuddle-math/{mathml,latex,uri}-inputs.json
// Outputs: extensions/websearch/testdata/defuddle-math/{mathml,latex,uri}-expected.json
import fs from 'node:fs'
import { createRequire } from 'node:module'
const require = createRequire((process.env.DFD_MATH_NODE_MODULES || new URL('./oracle/node_modules', import.meta.url).pathname) + '/')
const dir = new URL('../extensions/websearch/testdata/defuddle-math/', import.meta.url).pathname
const { MathMLToLaTeX } = require('mathml-to-latex')
const temml = require('temml')
const read = (n) => JSON.parse(fs.readFileSync(dir + n, 'utf8'))
const write = (n, v) => fs.writeFileSync(dir + n, JSON.stringify(v, null, 1) + '\n')

// mathml-to-latex as Defuddle calls it: a throw is recorded as null.
write('mathml-expected.json', read('mathml-inputs.json').map((xml) => {
  try { return { in: xml, out: MathMLToLaTeX.convert(xml) } } catch (e) { return { in: xml, out: null, err: String(e && e.message || e) } }
}))

// temml as math.full.js calls it: renderToString(latex, {displayMode, throwOnError:false}); a throw is recorded as null.
write('latex-expected.json', read('latex-inputs.json').flatMap((latex) => [false, true].map((display) => {
  try { return { latex, display, out: temml.renderToString(latex, { displayMode: display, throwOnError: false }) } } catch (e) { return { latex, display, out: null, err: String(e && e.message || e) } }
})))

// decodeURI and decodeURIComponent: the result, or the error name.
write('uri-expected.json', read('uri-inputs.json').map((s) => {
  const r = { in: s }
  for (const [k, f] of [['uri', decodeURI], ['component', decodeURIComponent]]) {
    try { r[k] = f(s) } catch (e) { r[k] = null; r[k + 'Err'] = e.name }
  }
  return r
}))
