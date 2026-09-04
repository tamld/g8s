## 2024-08-25 - Go String Suffix Matching Optimization
**Learning:** Checking string suffixes via `strings.ToLower()` followed by `strings.HasSuffix()` allocates a completely new copy of the string to lower case it on the heap. This causes massive memory allocation scaling with path length.
**Action:** Use `strings.EqualFold()` against a sliced substring instead, which performs a zero-allocation, case-insensitive comparison using shared underlying memory.
## 2025-03-09 - Avoid casting large strings to []rune for boundary operations
**Learning:** In Go, casting a large string (like a multi-megabyte log file or code file) to `[]rune` allocates a new array 4x the length of the string, causing huge memory spikes and GC pressure.
**Action:** When extracting substrings by rune count, first check for ASCII-only strings (`len(text) == utf8.RuneCountInString(text)`) to take a zero-allocation byte slice fast-path. For Unicode strings, iterate over the string using `range` and track byte indices manually instead of casting to `[]rune`.
## 2025-08-27 - string to []byte type conversion forces allocations
**Learning:** In Go, string to []byte casts are extremely common, but doing `[]byte(str)` forces a heap allocation since strings are immutable and the byte array needs to be mutable. Reversely, `string([]byte)` also forces allocation. This appends to strings very often (e.g. for generating truncation payloads or parsing search params).
**Action:** When a method returns a string built from byte arrays, avoid appending byte slices over and over. Instead, use a pre-sized `strings.Builder` using `sb.Grow(size)`, write all bytes directly to it using `sb.Write(bytes)`, and use `sb.String()` to return it without intermediate copy allocations.
## 2025-08-29 - Avoid strings.Builder and rune slice casting for simple substring extraction
**Learning:** Using `strings.Builder` and converting a string to `[]rune` to extract a substring by rune indexes allocates multiple times: once for the rune slice, and again for `builder.String()`. We can bypass this by tracking the starting index and byte size of the runes using `utf8.DecodeRuneInString` and slicing the original string directly, avoiding multiple allocations.
**Action:** Use `utf8.DecodeRuneInString` inside a loop over a string to find accurate slice bounds for multibyte characters rather than casting the string to a rune slice or relying on a builder, and slice the original string.
## 2026-09-04 - Zero-Allocation Case-Insensitive Prefix Checks
**Learning:** Using `strings.ToLower()` followed by `strings.HasPrefix()` (or `strings.HasSuffix()`) in hot paths or loops creates unnecessary string copies on the heap (e.g., 4 allocations per call).
**Action:** Replace `strings.HasPrefix(strings.ToLower(s), "prefix")` with a zero-allocation length guard and slice check: `len(s) >= len("prefix") && strings.EqualFold(s[:len("prefix")], "prefix")`.
