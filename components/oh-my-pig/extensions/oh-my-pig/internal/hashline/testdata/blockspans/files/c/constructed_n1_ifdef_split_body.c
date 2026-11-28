void f(int a) {
#ifdef X
    if (a) {
#else
    if (!a) {
#endif
        work();
    }
}

void g(void) {
    done();
}
