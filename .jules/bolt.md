## 2024-08-25 - Go String Suffix Matching Optimization
**Learning:** Checking string suffixes via `strings.ToLower()` followed by `strings.HasSuffix()` allocates a completely new copy of the string to lower case it on the heap. This causes massive memory allocation scaling with path length.
**Action:** Use `strings.EqualFold()` against a sliced substring instead, which performs a zero-allocation, case-insensitive comparison using shared underlying memory.
## 2025-03-09 - Avoid casting large strings to []rune for boundary operations
**Learning:** In Go, casting a large string (like a multi-megabyte log file or code file) to `[]rune` allocates a new array 4x the length of the string, causing huge memory spikes and GC pressure.
**Action:** When extracting substrings by rune count, first check for ASCII-only strings (`len(text) == utf8.RuneCountInString(text)`) to take a zero-allocation byte slice fast-path. For Unicode strings, iterate over the string using `range` and track byte indices manually instead of casting to `[]rune`.
