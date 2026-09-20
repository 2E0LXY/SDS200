/*
 * SDS200 UDP control client (port 50536).
 *
 * A single task owns the socket, so every request/response exchange is
 * strictly serialised. Other tasks queue commands and read snapshots.
 */
#pragma once
#include <stdbool.h>
#include <stdint.h>

#define STS_MAX_LINES   12
#define STS_COLS        30
#define WF_BINS         240
#define FQK_COUNT       100

typedef struct {
    char text[STS_COLS + 1];
    char mode[STS_COLS + 1];
    bool large;
} sts_line_t;

typedef struct {
    /* connection */
    bool online;
    char ip[16];
    char model[24];
    char last_error[64];

    /* GSI */
    uint32_t gsi_seq;
    char mode[32];
    char v_screen[32];
    char monitor_list[40];
    char system[64];
    char department[64];
    char site[64];
    char channel[64];
    char channel_tag[20];      /* element name, e.g. ConvFrequency */
    char frequency[24];
    char tgid[32];
    char unit_id[32];
    char modulation[16];
    char service_type[32];
    char system_hold[8];
    char department_hold[8];
    char site_hold[8];
    char channel_hold[8];
    int channel_index;         /* -1 unknown */
    bool func_on;              /* FUNC modifier active (Property F) */
    bool popup;                /* transient popup shown */
    int volume;                /* -1 unknown */
    int squelch;               /* -1 unknown */
    int rssi;                  /* dBm, 0 unknown */
    int signal;                /* 0..5, -1 unknown */
    char mute[12];
    char att[8];
    char rec[8];
    char p25_status[16];

    /* STS */
    uint32_t sts_seq;
    int sts_count;
    sts_line_t sts[STS_MAX_LINES];

    /* FQK */
    uint32_t fqk_seq;
    bool fqk_valid;
    uint8_t fqk[FQK_COUNT];

    /* Waterfall */
    bool wf_active;
    uint32_t wf_seq;
    uint8_t wf_bins[WF_BINS];

    /* Discovery */
    bool discovering;
    uint32_t discover_seq;
    char discovered_ip[16];
} scanner_state_t;

void scanner_init(void);
/* Copies the current state. */
void scanner_get_state(scanner_state_t *out);
/* Updates the target IP (also stored in NVS). */
void scanner_set_ip(const char *ip);

/* Queued commands. Return false if the queue is full. */
bool scanner_key(char code);
/* FUNC+key: presses F only if FUNC is not already active (e.g. Service Types = FUNC+Z). */
bool scanner_func(char code);
/* Next (+1) / previous (-1) channel via NXT/PRV with the current index; rotary fallback. */
bool scanner_step(int dir);
bool scanner_set_volume(int v);
bool scanner_set_squelch(int v);
bool scanner_fqk_refresh(void);
bool scanner_fqk_set(const uint8_t states[FQK_COUNT]);
bool scanner_waterfall(bool on);
bool scanner_discover(void);
/* Pauses polling (e.g. while offline or Wi-Fi is down). */
void scanner_set_network(bool up);
/* Leaves waterfall mode synchronously if active (used before reboot). */
void scanner_shutdown(void);
