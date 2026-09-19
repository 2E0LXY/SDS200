/*
 * Wi-Fi station management (ESP32-C6 via esp_wifi_remote / ESP-Hosted).
 */
#pragma once
#include <stdbool.h>
#include <stdint.h>

#define NET_MAX_SCAN 24

typedef struct {
    char ssid[33];
    int8_t rssi;
    bool secure;
} net_ap_t;

typedef struct {
    bool started;
    bool connected;      /* associated and has an IPv4 address */
    bool connecting;
    char ssid[33];
    char ip[16];
    char netmask[16];
    int8_t rssi;
    uint32_t disconnects;
    int last_reason;
} net_status_t;

typedef void (*net_link_cb_t)(bool up);

void net_init(void);
void net_set_link_cb(net_link_cb_t cb);
/* Connects with the given credentials (and remembers them in NVS). */
void net_connect(const char *ssid, const char *pass);
void net_status(net_status_t *out);
/* Starts an asynchronous scan; results become available via net_scan_results(). */
bool net_scan_start(void);
bool net_scan_busy(void);
/* Returns the number of results copied. seq increments on every completed scan. */
int net_scan_results(net_ap_t *out, int max, uint32_t *seq);
/* IPv4 address and netmask in host byte order; returns false if not connected. */
bool net_ipv4(uint32_t *ip, uint32_t *mask);
