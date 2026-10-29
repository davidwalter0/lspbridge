CC ?= cc
CFLAGS := -O2 -Wall

.PHONY: all clean test

all: app

app: main.o util.o
	$(CC) $(CFLAGS) -o $@ $^

%.o: %.c
	$(CC) $(CFLAGS) -c $<

test: app
	./app --self-test

clean:
	rm -f app *.o
