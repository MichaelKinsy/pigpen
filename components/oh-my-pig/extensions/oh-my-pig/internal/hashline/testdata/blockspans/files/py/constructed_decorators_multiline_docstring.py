@first
@second
def foo(
    a: int,
    b: int,
) -> int:
    """doc
col0
"""
# dedented comment
    return a + b

if a:
    x()
elif b:
    y()
else:
    z()

if a and \
   b:
    w()
