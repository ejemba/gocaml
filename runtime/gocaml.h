#if !defined GOCAML_H_INCLUDED
#define      GOCAML_H_INCLUDED

#include <stdint.h>

typedef int64_t gocaml_int;
typedef int gocaml_bool;
typedef double gocaml_float;

typedef struct {
    void *buf;
    gocaml_int size;
} gocaml_array;

#endif    // GOCAML_H_INCLUDED