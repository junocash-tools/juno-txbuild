#pragma once

#include <stddef.h>

#ifdef __cplusplus
extern "C" {
#endif

// Computes Orchard note commitment tree witness paths for requested positions.
//
// Returns a newly-allocated UTF-8 JSON string with one of:
//   - {"status":"ok","root":"...","paths":[{"position":n,"auth_path":["..",..]},...]}
//   - {"status":"err","error":"..."}
//
// The returned pointer must be freed with `juno_txbuild_string_free`.
char *juno_txbuild_orchard_witness_json(const char *req_json);

// Frees a string returned by `juno_txbuild_orchard_witness_json`.
void juno_txbuild_string_free(char *s);

#ifdef __cplusplus
} // extern "C"
#endif

