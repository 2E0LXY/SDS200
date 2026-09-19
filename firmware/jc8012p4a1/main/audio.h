/*
 * Scanner live audio: RTSP/RTP (PCMU, 8 kHz mono) to the ES8311 codec.
 *
 * The SDS200 allows only ONE RTSP session and a leaked session locks TCP 554
 * until the scanner is power-cycled, so every exit path sends TEARDOWN.
 */
#pragma once
#include <stdbool.h>
#include <stdint.h>

typedef enum {
    AUDIO_IDLE = 0,
    AUDIO_CONNECTING,
    AUDIO_BUFFERING,
    AUDIO_PLAYING,
    AUDIO_STOPPING,
    AUDIO_ERROR,
} audio_state_t;

typedef struct {
    audio_state_t state;
    uint32_t packets;
    uint32_t bytes;
    uint32_t lost;
    uint32_t underruns;
    int buffered_ms;
    char error[64];
} audio_status_t;

void audio_init(void);
void audio_start(void);
void audio_stop(void);
/* Stops the session and waits (up to timeout) until TEARDOWN has been sent. */
void audio_stop_blocking(int timeout_ms);
/* Called when Wi-Fi drops: the session is torn down immediately. */
void audio_network_down(void);
void audio_get_status(audio_status_t *out);
const char *audio_state_name(audio_state_t s);
void audio_set_local_volume(uint8_t percent);
