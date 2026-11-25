fn f(x: u32) {
    match x {
        0 => {},
        #[cfg(test)]
        1 => {
            work();
        },
        _ => {},
    }
}
