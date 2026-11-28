fetch(url).then((r) => {
  return r.json();
})
  .catch((e) => {
    log(e);
  });
