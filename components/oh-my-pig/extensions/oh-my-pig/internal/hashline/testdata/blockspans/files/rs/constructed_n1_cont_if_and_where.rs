fn f(a: bool, b: bool) {
    if a &&
        b {
        work();
    }
}

fn g<T>(x: T) -> T
where
    T: Clone {
    x
}
