const s = `hello { world`;
const re = /foo{1/g;
function ok() {
  return s + re;
}
