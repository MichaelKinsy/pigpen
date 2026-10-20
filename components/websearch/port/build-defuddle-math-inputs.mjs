// Builds the input lists of the math fixtures (deterministic). Run from components/websearch:
//   node port/build-defuddle-math-inputs.mjs
import fs from 'node:fs'
import { createRequire } from 'node:module'
const dir = new URL('../extensions/websearch/testdata/defuddle-math/', import.meta.url).pathname
const w = (n, v) => fs.writeFileSync(dir + n, JSON.stringify(v, null, 1) + '\n')
const m = (s) => `<math xmlns="http://www.w3.org/1998/Math/MathML">${s}</math>`

// ---- decodeURI / decodeURIComponent --------------------------------------------------------------------------------
const uri = ['', 'abc', '%41', '%2f', '%2F%3b%3A%23', '%E2%82%AC', '%e2%82%ac', '%F0%9F%98%80', '%', '%4', '%zz', '%C0%80', '%C1%81',
  '%E0%80%80', '%ED%A0%80', '%ED%BF%BF', '%F4%90%80%80', '%F8%88%80%80%80', '%80', '%BF', '%C3', '%C3%', '%C3%2', '%C3%28', '%E2%82', '%E2%82%A', '%E2%28%A1',
  '%c3%a9t%c3%a9', 'a%20b+c', '#fn:1', '#fn%3A1', '#cite_note-%C3%A9', '%25', '%2525', 'a%2Fb%2fc', '%3Cscript%3E', '%5Cfrac%7Ba%7D%7Bb%7D',
  '%5C', 'x%2By', '%e4%bd%a0%e5%a5%bd', '100%', '%%', '%u0041', 'ünï%C3%BC', '😀%F0%9F%98%80', '%ef%bf%bd', '%EF%BF%BE', '%F0%90%80%80', '%F4%8F%BF%BF',
  '%2', '%G1', '%1G', '\u0000%00', '%7f%80', '%FF', '%C2%A0', '%E3%80%80', '%0A%0D', 'a%', '%41%', '%41%4']
w('uri-inputs.json', uri)

// ---- MathML -----------------------------------------------------------------------------------------------------
const mathml = [
  m('<mi>x</mi>'), m('<mn>42</mn>'), m('<mo>+</mo>'), m('<mrow><mi>a</mi><mo>+</mo><mi>b</mi></mrow>'),
  m('<mrow><mi>x</mi><mo>=</mo><mn>1</mn></mrow>'), m('<mi>&#x3B1;</mi>'), m('<mi>α</mi><mo>∑</mo><mo>&#x2211;</mo>'),
  m('<mi mathvariant="bold">x</mi>'), m('<mi mathvariant="italic">x</mi>'), m('<mi mathvariant="bold-italic">x</mi>'),
  m('<mi mathvariant="double-struck">R</mi>'), m('<mi mathvariant="bold-fraktur">g</mi>'), m('<mi mathvariant="script">L</mi>'),
  m('<mi mathvariant="bold-script">L</mi>'), m('<mi mathvariant="fraktur">g</mi>'), m('<mi mathvariant="sans-serif">s</mi>'),
  m('<mi mathvariant="bold-sans-serif">s</mi>'), m('<mi mathvariant="sans-serif-italic">s</mi>'), m('<mi mathvariant="sans-serif-bold-italic">s</mi>'),
  m('<mi mathvariant="monospace">m</mi>'), m('<mi mathvariant="normal">m</mi>'),
  m('<mi>  x  </mi>'), m('<mi> </mi>'), m('<mi></mi>'), m('<mi>𝐀</mi><mi>é</mi><mi>ä</mi><mi>ǻ</mi><mi>ё</mi><mi>Ε</mi>'),
  m('<mfrac><mn>1</mn><mn>2</mn></mfrac>'), m('<mfrac bevelled="true"><mn>1</mn><mn>2</mn></mfrac>'),
  m('<mfrac bevelled="true"><mrow><mi>a</mi><mo>+</mo><mi>b</mi></mrow><mn>2</mn></mfrac>'),
  m('<mfrac linethickness="0"><mi>n</mi><mi>k</mi></mfrac>'), m('<mfrac linethickness="2pt"><mn>1</mn><mn>2</mn></mfrac>'),
  m('<mfrac linethickness="2"><mn>1</mn><mn>2</mn></mfrac>'), m('<mfrac linethickness="1"><mn>1</mn><mn>2</mn></mfrac>'),
  m('<mfrac linethickness="thin"><mn>1</mn><mn>2</mn></mfrac>'), m('<mfrac linethickness="thick"><mn>1</mn><mn>2</mn></mfrac>'),
  m('<mfrac linethickness="medium"><mn>1</mn><mn>2</mn></mfrac>'), m('<mfrac linethickness="3px"><mn>1</mn><mn>2</mn></mfrac>'),
  m('<mfrac linethickness="50%"><mn>1</mn><mn>2</mn></mfrac>'), m('<mfrac linethickness="0.5em"><mn>1</mn><mn>2</mn></mfrac>'),
  m('<mfrac linethickness=".25mm"><mn>1</mn><mn>2</mn></mfrac>'), m('<mfrac linethickness="-1pt"><mn>1</mn><mn>2</mn></mfrac>'),
  m('<mfrac linethickness="1.5 PT"><mn>1</mn><mn>2</mn></mfrac>'), m('<mfrac linethickness="2foo"><mn>1</mn><mn>2</mn></mfrac>'),
  m('<mfrac linethickness="abc"><mn>1</mn><mn>2</mn></mfrac>'), m('<mfrac linethickness="0px"><mn>1</mn><mn>2</mn></mfrac>'),
  m('<mfrac linethickness="1e3"><mn>1</mn><mn>2</mn></mfrac>'), m('<mfrac linethickness="0.123456789"><mn>1</mn><mn>2</mn></mfrac>'),
  m('<mfrac><mn>1</mn></mfrac>'), m('<mfrac><mn>1</mn><mn>2</mn><mn>3</mn></mfrac>'), m('<mfrac/>'),
  m('<msqrt><mn>2</mn></msqrt>'), m('<msqrt><mi>a</mi><mo>+</mo><mi>b</mi></msqrt>'), m('<mroot><mn>8</mn><mn>3</mn></mroot>'), m('<mroot><mn>8</mn></mroot>'),
  m('<msup><mi>x</mi><mn>2</mn></msup>'), m('<msub><mi>x</mi><mn>1</mn></msub>'), m('<msubsup><mi>x</mi><mn>1</mn><mn>2</mn></msubsup>'),
  m('<msup><mrow><mi>a</mi><mo>+</mo><mi>b</mi></mrow><mn>2</mn></msup>'), m('<msup><mrow><mi>a</mi></mrow><mn>2</mn></msup>'),
  m('<msup><mi>x</mi></msup>'), m('<msub><mi>x</mi><mn>1</mn><mn>2</mn></msub>'), m('<msubsup><mi>x</mi><mn>1</mn></msubsup>'),
  m('<msup><mrow><mi>ab</mi><mi>c</mi></mrow><mn>2</mn></msup>'), m('<msup><mrow><mi>ab</mi></mrow><mn>2</mn></msup>'),
  m('<munderover><mo>∑</mo><mrow><mi>i</mi><mo>=</mo><mn>0</mn></mrow><mi>n</mi></munderover>'), m('<munderover><mo>∑</mo><mi>i</mi></munderover>'),
  m('<mover><mi>x</mi><mo>^</mo></mover>'), m('<mover><mi>x</mi><mo>¯</mo></mover>'), m('<mover><mi>x</mi><mo>→</mo></mover>'),
  m('<munder><mi>x</mi><mo>_</mo></munder>'), m('<munder><mi>x</mi><mo>⏟</mo></munder>'), m('<mover><mi>x</mi><mo>⏞</mo></mover>'),
  m('<munder><mi>lim</mi><mrow><mi>x</mi><mo>→</mo><mn>0</mn></mrow></munder>'), m('<mover><mi>x</mi></mover>'),
  m('<mover><mi>x</mi><mover><mi>y</mi><mi>z</mi></mover></mover>'),
  m('<mmultiscripts><mi>X</mi><mprescripts/><mn>1</mn><mn>2</mn></mmultiscripts>'),
  m('<mmultiscripts><mi>F</mi><mn>1</mn><mn>2</mn><mn>3</mn><mn>4</mn></mmultiscripts>'),
  m('<mmultiscripts><mi>F</mi><none/><mn>2</mn><mprescripts/><mn>3</mn><none/></mmultiscripts>'),
  m('<mmultiscripts><mrow><mi>a</mi><mi>b</mi></mrow><mn>1</mn><mn>2</mn></mmultiscripts>'), m('<mmultiscripts><mrow></mrow><mn>1</mn></mmultiscripts>'),
  m('<mmultiscripts></mmultiscripts>'), m('<mmultiscripts><mi>X</mi><mn>1</mn></mmultiscripts>'),
  m('<mmultiscripts><mi>X</mi><mprescripts/><mn>1</mn><mn>2</mn><mn>3</mn><mn>4</mn></mmultiscripts>'),
  m('<mtext>hello world</mtext>'), m('<mtext mathvariant="bold">hi</mtext>'), m('<mtext mathvariant="italic">hi</mtext>'),
  m('<mtext mathvariant="bold-italic">hi</mtext>'), m('<mtext mathvariant="double-struck">R</mtext>'), m('<mtext mathvariant="monospace">code</mtext>'),
  m('<mtext mathvariant="fraktur">g</mtext>'), m('<mtext>a,b; c&#xA0;d</mtext>'), m('<mtext>é→x1</mtext>'), m('<mtext>😀 ok</mtext>'), m('<mtext></mtext>'),
  m('<mtable><mtr><mtd><mn>1</mn></mtd><mtd><mn>2</mn></mtd></mtr><mtr><mtd><mn>3</mn></mtd><mtd><mn>4</mn></mtd></mtr></mtable>'),
  m('<mfenced open="(" close=")"><mtable><mtr><mtd><mn>1</mn></mtd><mtd><mn>2</mn></mtd></mtr></mtable></mfenced>'),
  m('<mfenced open="[" close="]"><mtable><mtr><mtd><mn>1</mn></mtd></mtr></mtable></mfenced>'),
  m('<mfenced open="{" close="}"><mtable><mtr><mtd><mn>1</mn></mtd></mtr></mtable></mfenced>'),
  m('<mfenced open="|" close="|"><mtable><mtr><mtd><mn>1</mn></mtd></mtr></mtable></mfenced>'),
  m('<mfenced open="‖" close="‖"><mtable><mtr><mtd><mn>1</mn></mtd></mtr></mtable></mfenced>'),
  m('<mfenced open="⟨" close="⟩"><mtable><mtr><mtd><mn>1</mn></mtd></mtr></mtable></mfenced>'),
  m('<mfenced><mtable><mtr><mtd><mn>1</mn></mtd></mtr></mtable></mfenced>'),
  m('<mrow><mo>(</mo><mtable><mtr><mtd><mn>1</mn></mtd><mtd><mn>2</mn></mtd></mtr></mtable><mo>)</mo></mrow>'),
  m('<mrow><mo>{</mo><mtable><mtr><mtd><mi>x</mi></mtd><mtd><mtext>if</mtext></mtd></mtr><mtr><mtd><mi>y</mi></mtd><mtd><mtext>else</mtext></mtd></mtr></mtable></mrow>'),
  m('<mrow><mo>{</mo><mtable><mtr><mtd><mi>x</mi></mtd></mtr></mtable><mo></mo></mrow>'),
  m('<mtable columnalign="right left"><mtr><mtd><mi>a</mi></mtd><mtd><mo>=</mo><mi>b</mi></mtd></mtr><mtr><mtd><mi>c</mi></mtd><mtd><mo>=</mo><mi>d</mi></mtd></mtr></mtable>'),
  m('<mtable><mtr columnalign="right"><mtd columnalign="left"><mi>a</mi></mtd></mtr></mtable>'),
  m('<mtable><mtr><mtd><mtable><mtr><mtd><mn>1</mn></mtd></mtr></mtable></mtd></mtr></mtable>'),
  m('<mtr><mtd><mn>1</mn></mtd><mtd><mn>2</mn></mtd></mtr>'),
  m('<mfenced><mi>a</mi><mi>b</mi><mi>c</mi></mfenced>'), m('<mfenced open="[" close=")"><mi>a</mi><mi>b</mi></mfenced>'),
  m('<mfenced open="{" close="}"><mi>a</mi><mi>b</mi></mfenced>'), m('<mfenced open="||" close="||"><mi>a</mi></mfenced>'),
  m('<mfenced separators=";"><mi>a</mi><mi>b</mi><mi>c</mi></mfenced>'), m('<mfenced separators=""><mi>a</mi><mi>b</mi></mfenced>'),
  m('<mfenced separators=",;"><mi>a</mi><mi>b</mi><mi>c</mi><mi>d</mi></mfenced>'), m('<mfenced separators=" ,"><mi>a</mi><mi>b</mi><mi>c</mi></mfenced>'),
  m('<mfenced></mfenced>'), m('<mfenced open="" close=""><mi>a</mi></mfenced>'),
  m('<mrow><mo>(</mo><mi>a</mi><mo>+</mo><mi>b</mi><mo>)</mo></mrow>'), m('<mrow><mo>(</mo><mi>a</mi></mrow>'), m('<mrow><mi>a</mi><mo>)</mo></mrow>'),
  m('<mrow><mo>[</mo><mi>a</mi><mo>)</mo></mrow>'), m('<mrow><mo>|</mo><mi>x</mi><mo>|</mo></mrow>'), m('<mrow><mo>|</mo><mi>x</mi></mrow>'),
  m('<mrow><mo>‖</mo><mi>x</mi><mo>‖</mo></mrow>'), m('<mrow><mo>∥</mo><mi>x</mi><mo>∥</mo><mo>∥</mo><mi>y</mi><mo>∥</mo></mrow>'),
  m('<mrow><mo>||</mo><mi>x</mi><mo>||</mo></mrow>'), m('<mrow><mo>(</mo><mo>(</mo><mi>x</mi><mo>)</mo><mo>)</mo></mrow>'),
  m('<mrow><mo>(</mo><mo>[</mo><mi>x</mi><mo>)</mo></mrow>'), m('<mrow><mo>(</mo><mo>)</mo></mrow>'), m('<mo>(</mo><mi>x</mi><mo>)</mo>'),
  m('<mo>∥</mo><mi>P</mi><msubsup><mrow><mo>∥</mo></mrow><mi>F</mi><mn>2</mn></msubsup>'),
  m('<mo>|</mo><mi>P</mi><msub><mo>|</mo><mi>F</mi></msub><mo>+</mo><mo>|</mo><mi>Q</mi><mo>|</mo>'),
  m('<mo>|</mo><mi>P</mi><msup><mi>x</mi><mn>2</mn></msup>'),
  m('<mrow><mo>(</mo><mo>(</mo><mi>x</mi><mo>)</mo></mrow>'), m('<mrow><mo>)</mo><mi>x</mi><mo>(</mo></mrow>'),
  m('<maction actiontype="toggle"><mi>a</mi><mi>b</mi></maction>'), m('<maction><mi>a</mi><mi>b</mi></maction>'), m('<maction actiontype="statusline"><mi>a</mi><mi>b</mi></maction>'),
  m('<maction actiontype="x"></maction>'),
  ...['actuarial', 'radical', 'box', 'roundedbox', 'circle', 'left', 'right', 'top', 'bottom', 'updiagonalstrike', 'downdiagonalstrike', 'updiagonalarrow',
    'verticalstrike', 'horizontalstrike', 'madruwb', 'phasorangle', 'longdiv', 'foo'].map((n) => m(`<menclose notation="${n}"><mi>x</mi></menclose>`)),
  m('<menclose><mi>x</mi></menclose>'),
  m('<merror><mi>x</mi></merror>'), m('<mphantom><mi>x</mi></mphantom>'), m('<mpadded><mi>x</mi><mi>y</mi></mpadded>'),
  m('<mspace linebreak="newline"/>'), m('<mspace width="1em"/>'), m('<mi>a</mi><mspace/><mi>b</mi>'), m('<mstyle displaystyle="true"><mi>x</mi></mstyle>'),
  m('<semantics><mrow><mi>x</mi></mrow><annotation encoding="application/x-tex">x</annotation></semantics>'),
  m('<mi>&#x2061;</mi><mo>&#x2062;</mo><mo>&#x2064;</mo><mo>&#x2063;</mo>'), m('<mo>&#xA0;</mo>'), m('<mo>±</mo><mo>≤</mo><mo>∞</mo><mi>∞</mi><mo>…</mo><mi>ℏ</mi>'),
  m('<mn>½</mn><mn>²</mn><mn>₃</mn><mi>ⁿ</mi><mn>①</mn><mn>㊿</mn>'), m('<mo>²</mo>'), m('<mi>²</mi>'),
  m('<mi>sin</mi><mo>&#x2061;</mo><mi>x</mi>'), m('<mi>lim</mi>'), m('<mi>constructor</mi><mo>toString</mo><mn>valueOf</mn>'), m('<mi>__proto__</mi>'),
  m('<constructor><mi>x</mi></constructor>'), m('<toString/>'), m('<hasOwnProperty><mi>x</mi></hasOwnProperty>'), m('<__proto__/>'), m('<foo><mi>x</mi><mi>y</mi></foo>'),
  m('<math><mi>x</mi></math>'), m('<mrow><mi>x</mi></mrow><math><mi>y</mi></math>'),
  // wrapper and document level
  '', 'x', '<math/>', '<math></math>', '<math><mi>x</mi></math><math><mi>y</mi></math>', '<div><math><mi>x</mi></math></div>', '<p>a</p>',
  '<math><mi>x</mi></math> trailing', 'leading <math><mi>x</mi></math>', '<math><mi>x</mi></math></math>', '</math>', '<math><mi>x</mi>',
  '<math><mi>x</math>', '<math><mi>x</mi></mi></math>', '<?xml version="1.0"?><math><mi>x</mi></math>', '<?xml version="1.0" encoding="UTF-8"?>\n<math><mi>x</mi></math>',
  '<?xml version="2.0"?><math><mi>x</mi></math>', '<math><?pi data?><mi>x</mi></math>', '<math><!-- c --><mi>x</mi></math>', '<math><!-- c --></math>',
  '<math><![CDATA[x < y]]></math>', '<math><mi><![CDATA[x<y]]></mi></math>', '<math><mi>a<!-- c -->b</mi></math>', '<math><mi>a<?x y?>b</mi></math>',
  '<!DOCTYPE math><math><mi>x</mi></math>', '<!DOCTYPE math PUBLIC "-//W3C//DTD MathML 2.0//EN" "http://www.w3.org/Math/DTD/mathml2/mathml2.dtd"><math><mi>x</mi></math>',
  '<!DOCTYPE math [<!ENTITY foo "bar">]><math><mi>&foo;</mi></math>', '<!DOCTYPE html><math><mi>x</mi></math>',
  '<math><mi>x</mi></math><!DOCTYPE math>', '<!-- lead --><math><mi>x</mi></math><!-- trail -->',
  '<mml:math xmlns:mml="http://www.w3.org/1998/Math/MathML"><mml:mi>x</mml:mi></mml:math>',
  '<m:math xmlns:m="http://www.w3.org/1998/Math/MathML"><m:mi>x</m:mi></m:math>', '<m:math><m:mi>x</m:mi></m:math>', '<math xmlns:foo="a"><mi foo:bar="1">x</mi></math>',
  '<math><mi foo:bar="1">x</mi></math>', '<math><mi xml:lang="en">x</mi></math>', '<math xmlns="http://www.w3.org/1998/Math/MathML" xmlns:xlink="http://www.w3.org/1999/xlink"><mi xlink:href="#a">x</mi></math>',
  '<math xmlns=""><mi>x</mi></math>', '<math xmlns:a="u" xmlns:b="u"><mi a:x="1" b:x="2">x</mi></math>',
  // entities
  m('<mi>&amp;</mi><mo>&lt;</mo><mo>&gt;</mo><mo>&quot;</mo><mo>&apos;</mo>'), m('<mo>&nbsp;</mo>'), m('<mo>&unknown;</mo>'), m('<mo>&amp</mo>'), m('<mo>&#x41;&#65;&#X41;</mo>'),
  m('<mo>&#xZZ;</mo>'), m('<mo>&#;</mo>'), m('<mo>&</mo>'), m('<mo>a & b</mo>'), m('<mo>&#x1F600;</mo>'), m('<mo>&#xD800;</mo>'), m('<mo>&#0;</mo>'), m('<mo>&#x110000;</mo>'),
  m('<mo>&#99999999999;</mo>'), m('<mo>&#xA0;&#160;</mo>'), m('<mi>&#x3b1;&#x3A9;</mi>'), m('<mi>&InvisibleTimes;</mi>'), m('<mi a="&amp;&lt;">x</mi>'), m('<mi a="&bad;">x</mi>'),
  // attributes and quoting problems
  m('<mfenced open="{" close=>'), m("<mfenced open='{' close= ><mi>a</mi></mfenced>"), m('<mfenced open close="}"><mi>a</mi></mfenced>'),
  m('<mfenced open="(" close><mi>a</mi><mi>b</mi></mfenced>'), m('<mfenced open=( close=)><mi>a</mi></mfenced>'), m('<mfenced open=(><mi>a</mi></mfenced>'),
  m('<mi mathvariant=bold>x</mi>'), m('<mi mathvariant="bold" mathvariant="italic">x</mi>'), m('<mi mathvariant="bold"mathvariant2="x">x</mi>'),
  m('<mi a = "b" c = \'d\'>x</mi>'), m('<mi a="1"\n b="2">x</mi>'), m('<mi a="<">x</mi>'), m('<mi a="x>y">x</mi>'), m('<mi a=\'x"y\'>x</mi>'), m('<mi a="x'),
  m('<mfenced open="(" close=")" separators>'), m('<mfenced separators><mi>a</mi><mi>b</mi></mfenced>'),
  m('<mfenced separators=""><mi>a</mi><mi>b</mi></mfenced>'), m('<mi bevelled>x</mi>'), m('<mfrac bevelled><mn>1</mn><mn>2</mn></mfrac>'),
  m('<mfrac bevelled="bevelled"><mn>1</mn><mn>2</mn></mfrac>'), m('<mfrac bevelled="false"><mn>1</mn><mn>2</mn></mfrac>'), m('<mfrac bevelled=""><mn>1</mn><mn>2</mn></mfrac>'),
  m('<mfrac linethickness="linethickness"><mn>1</mn><mn>2</mn></mfrac>'),
  m('<mi 1a="x">x</mi>'), m('<mi a b c>x</mi>'), m('<mi =x>x</mi>'), m('<mi a="1"b="2">x</mi>'), m('<mi a="1"/>'), m('<mi/ >x</mi>'), m('<mi / >x</mi>'),
  m('<1mi>x</1mi>'), m('<mi>x<</mi>'), m('<mi>x< y</mi>'), m('<mi>a<b>c</mi>'), m('<<mi>x</mi>'), m('<mi>x</mi >'), m('<mi>x</ mi>'), m('<mi>x</mi\n>'),
  m('<MI>x</MI>'), m('<Mi>x</Mi>'), m('<mi>x</mi><mi>y</mi>'), m('<br>'), m('<mi>x</mi><br/>'), m('<mi>x</mi><br>'),
  // whitespace
  m('<mi>a</mi>\n<mo>+</mo>\r\n<mi>b</mi>'), m('<mrow> <mi>a</mi> <mo>+</mo> </mrow>'), m('<mtext>  a   b  </mtext>'), m('<mtext>a\u00a0b\u2003c</mtext>'),
  m('<mi>a\u2028b</mi>'), m('<mi>a\u0085b</mi>'), m('<mi>a\u0080b</mi>'), m('<mo>\t+\t</mo>'), m('<mn> 1 2 </mn>'), m('<mn>\u00a01\u00a0</mn>'),
  m('<mi\u0080a="1">x</mi>'), m('<mi a="1"\u0080b="2">x</mi>'),
  // deep nesting and larger
  m('<mrow>'.repeat(60) + '<mi>x</mi>' + '</mrow>'.repeat(60)), m('<msup>'.repeat(40) + '<mi>x</mi><mn>2</mn>'.repeat(1) + '<mn>2</mn></msup>'.repeat(40)),
  m('<mi>x</mi>'.repeat(200)),
]
// realistic page snippets: MathJax assistive MathML, Wikipedia, KaTeX, arXiv/LaTeXML
mathml.push(
  '<math xmlns="http://www.w3.org/1998/Math/MathML" display="block"><semantics><mrow><mfrac><mrow><mo>−</mo><mi>b</mi><mo>±</mo><msqrt><msup><mi>b</mi><mn>2</mn></msup><mo>−</mo><mn>4</mn><mi>a</mi><mi>c</mi></msqrt></mrow><mrow><mn>2</mn><mi>a</mi></mrow></mfrac></mrow><annotation encoding="application/x-tex">\\frac{-b\\pm\\sqrt{b^2-4ac}}{2a}</annotation></semantics></math>',
  '<math xmlns="http://www.w3.org/1998/Math/MathML" alttext="{\\displaystyle E=mc^{2}}" display="inline"><semantics><mrow class="MJX-TeXAtom-ORD"><mstyle displaystyle="true" scriptlevel="0"><mi>E</mi><mo>=</mo><mi>m</mi><msup><mi>c</mi><mrow class="MJX-TeXAtom-ORD"><mn>2</mn></mrow></msup></mstyle></mrow><annotation encoding="application/x-tex">{\\displaystyle E=mc^{2}}</annotation></semantics></math>',
  '<math display="block"><mrow><msub><mi>∫</mi><mrow><mn>0</mn></mrow></msub><msup><mrow></mrow><mi>∞</mi></msup><msup><mi>e</mi><mrow><mo>−</mo><msup><mi>x</mi><mn>2</mn></msup></mrow></msup><mi>d</mi><mi>x</mi><mo>=</mo><mfrac><msqrt><mi>π</mi></msqrt><mn>2</mn></mfrac></mrow></math>',
  '<math xmlns="http://www.w3.org/1998/Math/MathML" display="block"><mtable displaystyle="true" columnalign="right left right left right left right left right left right left" columnspacing="0em 2em 0em 2em 0em 2em 0em 2em 0em 2em 0em" rowspacing="3pt"><mtr><mtd><mi>a</mi></mtd><mtd><mo>=</mo><mn>1</mn></mtd></mtr><mtr><mtd><mi>b</mi></mtd><mtd><mo>=</mo><mn>2</mn></mtd></mtr></mtable></math>',
  '<math><mrow><mo>(</mo><mtable><mtr><mtd><mi>a</mi></mtd><mtd><mi>b</mi></mtd></mtr><mtr><mtd><mi>c</mi></mtd><mtd><mi>d</mi></mtd></mtr></mtable><mo>)</mo></mrow></math>',
  '<math xmlns="http://www.w3.org/1998/Math/MathML"><mrow><mi>sin</mi><mo>⁡</mo><mrow><mo>(</mo><mi>θ</mi><mo>)</mo></mrow><mo>=</mo><mfrac><mtext>opposite</mtext><mtext>hypotenuse</mtext></mfrac></mrow></math>',
  '<math xmlns="http://www.w3.org/1998/Math/MathML"><msubsup><mo>∫</mo><mn>0</mn><mn>1</mn></msubsup><mi>f</mi><mo stretchy="false">(</mo><mi>x</mi><mo stretchy="false">)</mo><mi>d</mi><mi>x</mi></math>',
  '<math><mrow><munderover><mo>∑</mo><mrow><mi>k</mi><mo>=</mo><mn>1</mn></mrow><mi>n</mi></munderover><msup><mi>k</mi><mn>2</mn></msup><mo>=</mo><mfrac><mrow><mi>n</mi><mo stretchy="false">(</mo><mi>n</mi><mo>+</mo><mn>1</mn><mo stretchy="false">)</mo><mo stretchy="false">(</mo><mn>2</mn><mi>n</mi><mo>+</mo><mn>1</mn><mo stretchy="false">)</mo></mrow><mn>6</mn></mfrac></mrow></math>',
  '<math xmlns="http://www.w3.org/1998/Math/MathML"><mi mathvariant="normal">&#x394;</mi><mi>G</mi><mo>=</mo><mi mathvariant="normal">&#x394;</mi><mi>H</mi><mo>&#x2212;</mo><mi>T</mi><mi mathvariant="normal">&#x394;</mi><mi>S</mi></math>',
  '<math><mi>a</mi><mo>&#x2260;</mo><mi>b</mi><mo>&#x2264;</mo><mi>c</mi><mo>&#x2192;</mo><mi>d</mi><mo>&#x21D2;</mo><mi>e</mi><mo>&#x2208;</mo><mi mathvariant="double-struck">R</mi></math>',
  '<math class="ltx_Math" display="inline" id="S1.p1.m1" alttext="x^{2}"><semantics id="S1.p1.m1.1a"><msup id="S1.p1.m1.1.1"><mi id="S1.p1.m1.1.1.2">x</mi><mn id="S1.p1.m1.1.1.3">2</mn></msup><annotation encoding="application/x-tex">x^{2}</annotation></semantics></math>',
  '<span class="katex-mathml"><math xmlns="http://www.w3.org/1998/Math/MathML"><semantics><mrow><mi>x</mi></mrow><annotation encoding="application/x-tex">x</annotation></semantics></math></span>',
  m('<mrow><mi>f</mi><mo>(</mo><mi>x</mi><mo>)</mo><mo>=</mo><mrow><mo>{</mo><mtable><mtr><mtd><mn>0</mn></mtd><mtd><mtext>if&#xA0;</mtext><mi>x</mi><mo>&lt;</mo><mn>0</mn></mtd></mtr><mtr><mtd><mn>1</mn></mtd><mtd><mtext>otherwise</mtext></mtd></mtr></mtable></mrow></mrow>'),
)
w('mathml-inputs.json', mathml)

// ---- LaTeX ------------------------------------------------------------------------------------------------------
// 1. the hand-written samples, 2. the LaTeX the Defuddle fixtures carry (data-latex, annotations, math scripts, alt of
// latex images), 3. systematic coverage generated from Temml's own tables (TEMML_SRC points at temml/src): every
// symbol, function, environment and macro with arguments.
const latex = new Set(fs.readFileSync(new URL('./defuddle-math-latex-samples.txt', import.meta.url).pathname, 'utf8').split('\n').filter((l) => l !== ''))
const fixtureDir = new URL('../extensions/websearch/testdata/defuddle/', import.meta.url).pathname
const decodeEntities = (t) => t.replace(/&(amp|lt|gt|quot|#39|#x27|apos);/g, (_, e) => ({ amp: '&', lt: '<', gt: '>', quot: '"', '#39': "'", '#x27': "'", apos: "'" })[e])
for (const f of fs.readdirSync(fixtureDir).filter((n) => n.endsWith('.html')).sort()) {
  const html = fs.readFileSync(fixtureDir + f, 'utf8')
  const found = []
  for (const re of [/data-(?:latex|math|tex)="([^"]+)"/g, /<annotation[^>]*encoding="application\/x-tex"[^>]*>([\s\S]*?)<\/annotation>/g,
    /<script[^>]*type="math\/tex[^"]*"[^>]*>([\s\S]*?)<\/script>/g, /<img[^>]*class="latex"[^>]*alt="([^"]+)"/g, /\bdata-entry="([^"]+)"/g]) {
    for (const m of html.matchAll(re)) found.push(decodeEntities(m[1]).trim())
  }
  for (const t of found) if (t && t.length < 600) latex.add(t)
}
if (process.env.TEMML_SRC) {
  const { register } = await import('node:module')
  const path = await import('node:path')
  const src = path.resolve(process.env.TEMML_SRC)
  register('data:text/javascript,' + encodeURIComponent(`
export async function resolve(specifier, context, next) {
  try { return await next(specifier, context) } catch (e) {
    if (specifier.startsWith('.') && !specifier.endsWith('.js')) return next(specifier + '.js', context)
    throw e
  }
}`), import.meta.url)
  const symbols = (await import(path.join(src, 'symbols.js'))).default
  const { _functions } = await import(path.join(src, 'defineFunction.js'))
  await import(path.join(src, 'functions.js'))
  const { _environments } = await import(path.join(src, 'defineEnvironment.js'))
  const { _macros } = await import(path.join(src, 'defineMacro.js'))
  for (const sym of Object.keys(symbols.math)) latex.add(`a ${sym} b`)
  for (const sym of Object.keys(symbols.math)) if (sym.startsWith('\\')) latex.add(`${sym}`)
  for (const sym of Object.keys(symbols.text)) latex.add(`\\text{a ${sym} b}`)
  for (const [name, data] of Object.entries(_functions)) {
    if (!name.startsWith('\\')) continue
    const n = data.numArgs, o = data.numOptionalArgs
    const args = (k) => Array.from({ length: k }, (_, i) => '{' + 'abc'[i % 3] + '}').join('')
    latex.add(name)
    if (n > 0) {
      latex.add(name + args(n))
      latex.add(name + '{x^2}'.repeat(n))
      latex.add('x ' + name + ' y' + args(Math.max(0, n - 1)))
    }
    if (o > 0) latex.add(name + '[opt]' + args(n))
    if (n === 1) { latex.add(name + '{\\alpha}'); latex.add(name + '{AB}'); latex.add('\\text{' + name + '{a}}'); latex.add(name + '{a}^2_3') }
    if (n === 2) { latex.add(name + '{12}{34}'); latex.add(name + '{\\frac ab}{c}') }
  }
  for (const name of Object.keys(_environments)) {
    latex.add(`\\begin{${name}} a & b \\\\ c & d \\end{${name}}`)
    latex.add(`\\begin{${name}}{cc} a & b \\\\ c & d \\end{${name}}`)
    latex.add(`\\begin{${name}} a \\end{${name}}`)
  }
  for (const [name, v] of Object.entries(_macros)) {
    if (!name.startsWith('\\')) { latex.add(`a ${name} b`); continue }
    latex.add(name)
    latex.add(name + '{a}')
    latex.add(name + '{a}{b}')
    latex.add(name + '{a}{b}{c}')
    latex.add('x ' + name + ' y')
  }
}
if (process.env.TEMML_DIST) {
  const dist = fs.readFileSync(process.env.TEMML_DIST, 'utf8')
  for (const m of dist.matchAll(/defineMacro\("([^"]+)"/g)) {
    const name = m[1]
    latex.add(name); latex.add(name + '{a}'); latex.add(name + '{a}{b}'); latex.add(name + '{a}{b}{c}'); latex.add('x ' + name + ' y')
  }
}
w('latex-inputs.json', [...latex])
