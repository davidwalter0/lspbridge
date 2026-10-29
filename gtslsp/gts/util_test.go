package gts

import "unicode/utf16"

func utf16Units(s string) []uint16 { return utf16.Encode([]rune(s)) }

func decodeUTF16(u []uint16) []rune { return utf16.Decode(u) }
