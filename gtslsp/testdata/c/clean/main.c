#include <stdio.h>

#define MAX_ITEMS 16

typedef struct point {
    int x;
    int y;
} point_t;

struct node {
    struct node *next;
    int value;
};

static int counter = 0;

/* add two ints */
int add(int a, int b)
{
    return a + b;
}

int main(void)
{
    struct node n = {0, 1};
    point_t p = {1, 2};
    counter = add(p.x, n.value);
    printf("%d\n", counter);
    return 0;
}
