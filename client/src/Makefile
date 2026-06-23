CC ?= cc
CFLAGS ?= -O2 -Wall -Wextra
CPPFLAGS ?= -I.
LDFLAGS ?=
LDLIBS ?=

TARGET := fssrl-client
SOURCES := main.c communication.c
OBJECTS := $(SOURCES:.c=.o)

.PHONY: all clean

all: $(TARGET)

$(TARGET): $(OBJECTS)
	$(CC) $(LDFLAGS) -o $@ $^ $(LDLIBS)

%.o: %.c server-side-ratelimit.h packet.h
	$(CC) $(CPPFLAGS) $(CFLAGS) -c -o $@ $<

clean:
	rm -f $(OBJECTS) $(TARGET)
