fn f(x: Option<i32>) -> String {
    match x {
        Some(v) => v,
        None => 0,
    }
        .to_string()
}
