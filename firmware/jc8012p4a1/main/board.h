/*
 * Board support: I2C bus, MIPI-DSI display + LVGL port, GSL3680 touch,
 * backlight PWM and the ES8311 audio path.
 */
#pragma once
#include <stdbool.h>
#include <stdint.h>
#include <stddef.h>
#include "esp_err.h"
#include "lvgl.h"

esp_err_t board_i2c_init(void);
esp_err_t board_backlight_init(void);
void board_backlight_set(uint8_t percent);

/* Brings up the panel (revision 1 or 2), LVGL port with PPA rotation, and touch. */
lv_display_t *board_display_init(uint8_t panel_rev, bool flip);
/* Changes the landscape orientation at runtime (touch follows automatically). */
void board_display_set_flip(bool flip);

/* ES8311 playback: 16 kHz, 16-bit, stereo frames. */
esp_err_t board_audio_init(void);
esp_err_t board_audio_start(void);
void board_audio_stop(void);
/* Writes interleaved stereo PCM16; blocks until queued to I2S DMA. */
esp_err_t board_audio_write(const int16_t *frames, size_t n_frames);
void board_audio_set_volume(uint8_t percent);
