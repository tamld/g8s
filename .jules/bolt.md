## 2024-08-25 - Go String Suffix Matching Optimization
**Learning:** Checking string suffixes via `strings.ToLower()` followed by `strings.HasSuffix()` allocates a completely new copy of the string to lower case it on the heap. This causes massive memory allocation scaling with path length.
**Action:** Use `strings.EqualFold()` against a sliced substring instead, which performs a zero-allocation, case-insensitive comparison using shared underlying memory.
