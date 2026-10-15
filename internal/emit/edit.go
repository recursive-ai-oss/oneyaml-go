package emit

// Exported scalar writers for the edit engine. They add to the encoder's
// output code without changing it.

// AppendSingle appends s as a single-quoted scalar. The caller must have checked
// that s holds no line break and no unprintable character.
func AppendSingle(dst []byte, s string) []byte { return appendSingle(dst, s) }

// AppendDouble appends s as a double-quoted scalar, escaping whatever needs it.
// Any string can be written this way.
func AppendDouble(dst []byte, s string) []byte { return appendDouble(dst, s) }

// AppendFloat appends f in the encoder's shortest form that reads back as the
// same float.
func AppendFloat(dst []byte, f float64, bits int) []byte { return appendFloat(dst, f, bits) }
