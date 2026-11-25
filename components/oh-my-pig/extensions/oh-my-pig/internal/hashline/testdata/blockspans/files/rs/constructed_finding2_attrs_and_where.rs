#[inline]
fn foo<T>(x: T) -> T
where
    T: Clone,
{
    x
}

/// Doc.
pub fn bar(
    a: u32,
) -> u32
where
    u32: Copy,
{
    a
}

#[allow(dead_code)]
const X: &[u8] =
    &[1, 2, 3];

#[allow(dead_code)]
const Y: u32 = 1;
