/*
 * Persistent settings (NVS namespace "sds200").
 */
#pragma once
#include <stdbool.h>
#include <stdint.h>

typedef struct {
    char wifi_ssid[33];
    char wifi_pass[65];
    char scanner_ip[16];
    uint8_t backlight;      /* 5..100 % */
    uint8_t panel_rev;      /* 1 = original, 2 = V2 */
    bool flip;              /* rotate the landscape UI by 180 degrees */
    uint8_t local_volume;   /* 0..100 % codec output volume */
} app_settings_t;

void settings_init(void);
/* Returns a copy of the current settings. */
app_settings_t settings_get(void);
void settings_set_wifi(const char *ssid, const char *pass);
void settings_set_scanner_ip(const char *ip);
void settings_set_backlight(uint8_t percent);
void settings_set_panel_rev(uint8_t rev);
void settings_set_flip(bool flip);
void settings_set_local_volume(uint8_t vol);
