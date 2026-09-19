/*
 * RTSP client + RTP receiver + jitter buffer + I2S playback.
 *
 * Session: OPTIONS -> DESCRIBE -> SETUP -> PLAY, GET_PARAMETER keepalive every
 * 15 s, TEARDOWN on every exit path. After SETUP (and with each keepalive) a
 * 4-byte datagram CE FA ED FE is sent from the RTP socket to the scanner's
 * RTP source port to open stateful firewalls/NAT.
 */
#include <string.h>
#include <stdio.h>
#include <stdlib.h>
#include <strings.h>
#include <errno.h>
#include <fcntl.h>
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/event_groups.h"
#include "freertos/semphr.h"
#include "esp_log.h"
#include "esp_timer.h"
#include "esp_random.h"
#include "lwip/sockets.h"
#include "lwip/inet.h"
#include "app_config.h"
#include "settings.h"
#include "board.h"
#include "audio.h"

static const char *TAG = "audio";

#define EV_START        BIT0
#define EV_STOP         BIT1
#define EV_IDLE         BIT2    /* set whenever no session is active */

#define RING_SAMPLES    8000    /* 1 s at 8 kHz */
#define PREBUFFER       1600    /* 200 ms */
#define MAX_BUFFER      4000    /* 500 ms: beyond this, drop to limit latency */
#define CHUNK           160     /* 20 ms at 8 kHz */
#define KEEPALIVE_MS    15000
#define RTP_SILENCE_MS  10000
#define RTSP_TIMEOUT_MS 3000

static EventGroupHandle_t s_ev;
static SemaphoreHandle_t s_lock;
static audio_status_t s_st;

static int16_t s_ring[RING_SAMPLES];
static int s_head, s_tail, s_count;
static volatile bool s_want_play;

static void lock(void) { xSemaphoreTake(s_lock, portMAX_DELAY); }
static void unlock(void) { xSemaphoreGive(s_lock); }
static int64_t now_ms(void) { return esp_timer_get_time() / 1000; }

static void set_state(audio_state_t st, const char *err)
{
    lock();
    s_st.state = st;
    if (err) {
        strlcpy(s_st.error, err, sizeof(s_st.error));
    } else if (st == AUDIO_CONNECTING) {
        s_st.error[0] = 0;
    }
    unlock();
}

/* ------------------------------------------------------------- G.711 u -- */

static inline int16_t ulaw_to_pcm(uint8_t u)
{
    u = ~u;
    int t = ((u & 0x0F) << 3) + 0x84;
    t <<= (u & 0x70) >> 4;
    return (int16_t)((u & 0x80) ? (0x84 - t) : (t - 0x84));
}

static void ring_push(const uint8_t *ulaw, int n)
{
    lock();
    for (int i = 0; i < n; i++) {
        if (s_count == RING_SAMPLES) {       /* overflow: drop oldest */
            s_tail = (s_tail + 1) % RING_SAMPLES;
            s_count--;
        }
        s_ring[s_head] = ulaw_to_pcm(ulaw[i]);
        s_head = (s_head + 1) % RING_SAMPLES;
        s_count++;
    }
    unlock();
}

static void ring_clear(void)
{
    lock();
    s_head = s_tail = s_count = 0;
    unlock();
}

/* --------------------------------------------------------- playback task -- */

static void playback_task(void *arg)
{
    (void)arg;
    static int16_t in[CHUNK];
    static int16_t out[CHUNK * 2 * 2];     /* 2x upsample, stereo */
    bool open = false;
    bool buffering = true;
    int16_t last = 0;

    for (;;) {
        if (!s_want_play) {
            if (open) {
                board_audio_stop();
                open = false;
            }
            vTaskDelay(pdMS_TO_TICKS(50));
            continue;
        }
        if (!open) {
            if (board_audio_start() != ESP_OK) {
                ESP_LOGE(TAG, "codec start failed");
                vTaskDelay(pdMS_TO_TICKS(500));
                continue;
            }
            open = true;
            buffering = true;
            last = 0;
        }

        int got = 0;
        lock();
        if (buffering && s_count >= PREBUFFER) {
            buffering = false;
            if (s_st.state == AUDIO_BUFFERING) {
                s_st.state = AUDIO_PLAYING;
            }
        }
        if (!buffering) {
            while (s_count > MAX_BUFFER) {        /* clock drift: trim latency */
                s_tail = (s_tail + CHUNK) % RING_SAMPLES;
                s_count -= CHUNK;
            }
            if (s_count >= CHUNK) {
                for (int i = 0; i < CHUNK; i++) {
                    in[i] = s_ring[s_tail];
                    s_tail = (s_tail + 1) % RING_SAMPLES;
                }
                s_count -= CHUNK;
                got = CHUNK;
            } else {
                buffering = true;
                s_st.underruns++;
                if (s_st.state == AUDIO_PLAYING) {
                    s_st.state = AUDIO_BUFFERING;
                }
            }
        }
        s_st.buffered_ms = s_count / 8;
        unlock();

        if (got) {
            /* 8 kHz -> 16 kHz linear interpolation, duplicated to both channels */
            for (int i = 0; i < CHUNK; i++) {
                int16_t mid = (int16_t)(((int)last + in[i]) / 2);
                out[i * 4 + 0] = mid;
                out[i * 4 + 1] = mid;
                out[i * 4 + 2] = in[i];
                out[i * 4 + 3] = in[i];
                last = in[i];
            }
        } else {
            memset(out, 0, sizeof(out));
            last = 0;
        }
        if (board_audio_write(out, CHUNK * 2) != ESP_OK) {
            vTaskDelay(pdMS_TO_TICKS(20));
        }
    }
}

/* ------------------------------------------------------------------ RTSP -- */

typedef struct {
    int tcp;
    int rtp;
    int cseq;
    char ip[16];
    char aggregate[96];
    char content_base[160];
    char track[200];
    char session[80];
    uint16_t client_port;
    uint16_t server_port;
    struct in_addr source;
    char buf[4096];
    int len;
} rtsp_t;

static bool wait_fd(int fd, bool write, int timeout_ms)
{
    fd_set set;
    FD_ZERO(&set);
    FD_SET(fd, &set);
    struct timeval tv = {.tv_sec = timeout_ms / 1000, .tv_usec = (timeout_ms % 1000) * 1000};
    int r = select(fd + 1, write ? NULL : &set, write ? &set : NULL, NULL, &tv);
    return r > 0;
}

static bool tcp_connect(rtsp_t *c)
{
    c->tcp = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
    if (c->tcp < 0) {
        return false;
    }
    struct sockaddr_in a = {
        .sin_family = AF_INET,
        .sin_port = htons(SCANNER_RTSP_PORT),
    };
    inet_aton(c->ip, &a.sin_addr);
    int fl = fcntl(c->tcp, F_GETFL, 0);
    fcntl(c->tcp, F_SETFL, fl | O_NONBLOCK);
    int r = connect(c->tcp, (struct sockaddr *)&a, sizeof(a));
    if (r < 0 && errno != EINPROGRESS) {
        return false;
    }
    if (r < 0) {
        if (!wait_fd(c->tcp, true, RTSP_TIMEOUT_MS)) {
            return false;
        }
        int err = 0;
        socklen_t el = sizeof(err);
        getsockopt(c->tcp, SOL_SOCKET, SO_ERROR, &err, &el);
        if (err) {
            errno = err;
            return false;
        }
    }
    fcntl(c->tcp, F_SETFL, fl & ~O_NONBLOCK);
    int one = 1;
    setsockopt(c->tcp, IPPROTO_TCP, TCP_NODELAY, &one, sizeof(one));
    return true;
}

static bool tcp_send_all(int fd, const char *p, int n)
{
    while (n > 0) {
        int w = send(fd, p, n, 0);
        if (w <= 0) {
            return false;
        }
        p += w;
        n -= w;
    }
    return true;
}

/* Case-insensitive header lookup inside c->buf (headers only). */
static bool header(const rtsp_t *c, const char *name, char *out, size_t len)
{
    size_t nl = strlen(name);
    const char *p = c->buf;
    const char *end = strstr(c->buf, "\r\n\r\n");
    if (!end) {
        return false;
    }
    while (p && p < end) {
        const char *eol = strstr(p, "\r\n");
        if (!eol) {
            break;
        }
        if ((size_t)(eol - p) > nl && strncasecmp(p, name, nl) == 0 && p[nl] == ':') {
            const char *v = p + nl + 1;
            while (*v == ' ') {
                v++;
            }
            size_t vl = eol - v;
            if (vl >= len) {
                vl = len - 1;
            }
            memcpy(out, v, vl);
            out[vl] = 0;
            return true;
        }
        p = eol + 2;
    }
    return false;
}

/* Reads one complete response into c->buf. Returns the status code or -1. */
static int read_response(rtsp_t *c, int timeout_ms)
{
    c->len = 0;
    c->buf[0] = 0;
    const int64_t deadline = now_ms() + timeout_ms;
    int body_need = -1;
    char *hdr_end = NULL;
    for (;;) {
        if (hdr_end) {
            int have = c->len - (int)(hdr_end + 4 - c->buf);
            if (have >= body_need) {
                break;
            }
        }
        int remain = (int)(deadline - now_ms());
        if (remain <= 0 || !wait_fd(c->tcp, false, remain)) {
            return -1;
        }
        int r = recv(c->tcp, c->buf + c->len, sizeof(c->buf) - 1 - c->len, 0);
        if (r <= 0) {
            return -1;
        }
        c->len += r;
        c->buf[c->len] = 0;
        if (!hdr_end) {
            hdr_end = strstr(c->buf, "\r\n\r\n");
            if (hdr_end) {
                char cl[16];
                body_need = header(c, "Content-Length", cl, sizeof(cl)) ? atoi(cl) : 0;
                if (body_need < 0 || body_need > (int)sizeof(c->buf) - 1024) {
                    return -1;
                }
            }
        }
        if (c->len >= (int)sizeof(c->buf) - 1) {
            return -1;
        }
    }
    int code = -1;
    if (strncmp(c->buf, "RTSP/1.0 ", 9) == 0) {
        code = atoi(c->buf + 9);
    }
    return code;
}

static bool send_request(rtsp_t *c, const char *method, const char *uri, const char *extra)
{
    char req[640];
    c->cseq++;
    int n = snprintf(req, sizeof(req), "%s %s RTSP/1.0\r\nCSeq: %d\r\nUser-Agent: SDS200-Panel\r\n%s%s%s\r\n",
                     method, uri, c->cseq,
                     c->session[0] ? "Session: " : "", c->session[0] ? c->session : "",
                     c->session[0] ? "\r\n" : "");
    if (n <= 0 || n >= (int)sizeof(req) - 2) {
        return false;
    }
    /* insert extra headers before the blank line */
    if (extra && extra[0]) {
        req[n - 2] = 0;
        n = strlcat(req, extra, sizeof(req));
        n = strlcat(req, "\r\n", sizeof(req));
        if (n >= (int)sizeof(req)) {
            return false;
        }
    }
    ESP_LOGD(TAG, "> %s %s", method, uri);
    return tcp_send_all(c->tcp, req, n);
}

static int request(rtsp_t *c, const char *method, const char *uri, const char *extra)
{
    if (!send_request(c, method, uri, extra)) {
        return -1;
    }
    int code = read_response(c, RTSP_TIMEOUT_MS);
    ESP_LOGI(TAG, "%s -> %d", method, code);
    return code;
}

static void punch(rtsp_t *c)
{
    if (c->rtp < 0 || !c->server_port) {
        return;
    }
    static const uint8_t pkt[4] = {0xCE, 0xFA, 0xED, 0xFE};
    struct sockaddr_in to = {
        .sin_family = AF_INET,
        .sin_port = htons(c->server_port),
        .sin_addr = c->source,
    };
    sendto(c->rtp, pkt, sizeof(pkt), 0, (struct sockaddr *)&to, sizeof(to));
}

static bool open_rtp(rtsp_t *c)
{
    c->rtp = socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
    if (c->rtp < 0) {
        return false;
    }
    int rcvbuf = 16384;
    setsockopt(c->rtp, SOL_SOCKET, SO_RCVBUF, &rcvbuf, sizeof(rcvbuf));
    for (int i = 0; i < 16; i++) {
        uint16_t port = (uint16_t)((40000 + (esp_random() % 20000)) & ~1u);   /* even, RTP convention */
        struct sockaddr_in a = {
            .sin_family = AF_INET,
            .sin_port = htons(port),
            .sin_addr.s_addr = htonl(INADDR_ANY),
        };
        if (bind(c->rtp, (struct sockaddr *)&a, sizeof(a)) == 0) {
            c->client_port = port;
            return true;
        }
    }
    return false;
}

static void parse_transport(rtsp_t *c, const char *t)
{
    const char *s = strstr(t, "source=");
    if (s) {
        char ip[16];
        int i = 0;
        for (s += 7; *s && *s != ';' && i < 15; s++) {
            ip[i++] = *s;
        }
        ip[i] = 0;
        struct in_addr a;
        if (inet_aton(ip, &a)) {
            c->source = a;
        }
    }
    const char *sp = strstr(t, "server_port=");
    if (sp) {
        c->server_port = (uint16_t)atoi(sp + 12);
    }
}

static bool parse_sdp_control(const char *body, char *track, size_t len)
{
    bool in_audio = false, pcmu = false;
    track[0] = 0;
    const char *p = body;
    while (p && *p) {
        const char *eol = strpbrk(p, "\r\n");
        size_t ll = eol ? (size_t)(eol - p) : strlen(p);
        if (ll > 2 && strncmp(p, "m=", 2) == 0) {
            in_audio = strncmp(p, "m=audio", 7) == 0 && memmem(p, ll, "RTP/AVP", 7);
            if (in_audio) {
                /* payload type 0 must be among the formats */
                const char *f = memmem(p, ll, "RTP/AVP", 7) + 7;
                while (f < p + ll) {
                    while (f < p + ll && *f == ' ') {
                        f++;
                    }
                    if (f < p + ll && *f == '0' && (f + 1 == p + ll || f[1] == ' ')) {
                        pcmu = true;
                    }
                    while (f < p + ll && *f != ' ') {
                        f++;
                    }
                }
            }
        } else if (in_audio && ll > 10 && strncmp(p, "a=control:", 10) == 0) {
            size_t vl = ll - 10;
            if (vl >= len) {
                vl = len - 1;
            }
            memcpy(track, p + 10, vl);
            track[vl] = 0;
        }
        if (!eol) {
            break;
        }
        p = eol + 1;
        while (*p == '\r' || *p == '\n') {
            p++;
        }
    }
    return pcmu && track[0];
}

static void teardown(rtsp_t *c)
{
    if (c->tcp >= 0 && c->session[0]) {
        char uri[112];
        snprintf(uri, sizeof(uri), "%s/", c->aggregate);
        int code = request(c, "TEARDOWN", uri, NULL);
        ESP_LOGI(TAG, "TEARDOWN sent (status %d)", code);
    }
    c->session[0] = 0;
}

static void close_all(rtsp_t *c)
{
    if (c->tcp >= 0) {
        shutdown(c->tcp, SHUT_RDWR);
        close(c->tcp);
        c->tcp = -1;
    }
    if (c->rtp >= 0) {
        close(c->rtp);
        c->rtp = -1;
    }
}

static bool stop_requested(void)
{
    return (xEventGroupGetBits(s_ev) & EV_STOP) != 0;
}

static void run_session(rtsp_t *c)
{
    char extra[160], uri[240], hv[160];
    int code;

    set_state(AUDIO_CONNECTING, NULL);
    snprintf(c->aggregate, sizeof(c->aggregate), "rtsp://%s/au:scanner.au", c->ip);
    inet_aton(c->ip, &c->source);

    if (!open_rtp(c)) {
        set_state(AUDIO_ERROR, "RTP socket failed");
        return;
    }
    if (!tcp_connect(c)) {
        ESP_LOGE(TAG, "TCP 554 connect failed (errno %d)", errno);
        set_state(AUDIO_ERROR, "RTSP connect failed");
        return;
    }
    if ((code = request(c, "OPTIONS", c->aggregate, NULL)) != 200) {
        set_state(AUDIO_ERROR, "RTSP OPTIONS failed");
        return;
    }
    if ((code = request(c, "DESCRIBE", c->aggregate, "Accept: application/sdp\r\n")) != 200) {
        set_state(AUDIO_ERROR, "RTSP DESCRIBE failed");
        return;
    }
    if (!header(c, "Content-Base", c->content_base, sizeof(c->content_base)) || !c->content_base[0]) {
        snprintf(c->content_base, sizeof(c->content_base), "%s/", c->aggregate);
    }
    char *body = strstr(c->buf, "\r\n\r\n");
    char track[128];
    if (!body || !parse_sdp_control(body + 4, track, sizeof(track))) {
        set_state(AUDIO_ERROR, "SDP: no PCMU audio track");
        return;
    }
    if (strncmp(track, "rtsp://", 7) == 0) {
        strlcpy(c->track, track, sizeof(c->track));
    } else {
        size_t bl = strlen(c->content_base);
        while (bl > 0 && c->content_base[bl - 1] == '/') {
            c->content_base[--bl] = 0;
        }
        const char *t = track;
        while (*t == '/') {
            t++;
        }
        snprintf(c->track, sizeof(c->track), "%s/%s", c->content_base, t);
    }

    snprintf(extra, sizeof(extra), "Transport: RTP/AVP;unicast;client_port=%u\r\n", c->client_port);
    if ((code = request(c, "SETUP", c->track, extra)) != 200) {
        set_state(AUDIO_ERROR, "RTSP SETUP failed");
        return;
    }
    if (!header(c, "Session", hv, sizeof(hv)) || !hv[0]) {
        set_state(AUDIO_ERROR, "SETUP: no Session");
        return;
    }
    char *semi = strchr(hv, ';');
    if (semi) {
        *semi = 0;
    }
    strlcpy(c->session, hv, sizeof(c->session));
    if (header(c, "Transport", hv, sizeof(hv))) {
        parse_transport(c, hv);
    }
    ESP_LOGI(TAG, "SETUP ok: session %s client_port %u server_port %u", c->session, c->client_port,
             c->server_port);
    punch(c);

    snprintf(uri, sizeof(uri), "%s/", c->aggregate);
    if ((code = request(c, "PLAY", uri, "Range: npt=0.000-\r\n")) != 200) {
        set_state(AUDIO_ERROR, "RTSP PLAY failed");
        return;   /* caller sends TEARDOWN */
    }

    ring_clear();
    lock();
    s_st.packets = s_st.bytes = s_st.lost = s_st.underruns = 0;
    s_st.state = AUDIO_BUFFERING;
    unlock();
    s_want_play = true;

    static uint8_t pkt[1600];
    int64_t last_keep = now_ms();
    int64_t last_rtp = now_ms();
    bool have_seq = false;
    uint16_t exp_seq = 0;

    while (!stop_requested()) {
        fd_set rs;
        FD_ZERO(&rs);
        FD_SET(c->rtp, &rs);
        FD_SET(c->tcp, &rs);
        int maxfd = c->rtp > c->tcp ? c->rtp : c->tcp;
        struct timeval tv = {.tv_sec = 0, .tv_usec = 100000};
        int r = select(maxfd + 1, &rs, NULL, NULL, &tv);
        int64_t t = now_ms();

        if (r > 0 && FD_ISSET(c->rtp, &rs)) {
            struct sockaddr_in from;
            socklen_t fl = sizeof(from);
            int n = recvfrom(c->rtp, pkt, sizeof(pkt), 0, (struct sockaddr *)&from, &fl);
            if (n >= 12 && (pkt[0] >> 6) == 2) {
                int cc = pkt[0] & 0x0F;
                int off = 12 + cc * 4;
                if (pkt[0] & 0x10) {             /* header extension */
                    if (n >= off + 4) {
                        off += 4 + 4 * ((pkt[off + 2] << 8) | pkt[off + 3]);
                    }
                }
                int end = n;
                if (pkt[0] & 0x20) {             /* padding */
                    end -= pkt[n - 1];
                }
                uint8_t pt = pkt[1] & 0x7F;
                uint16_t seq = (uint16_t)((pkt[2] << 8) | pkt[3]);
                if (pt == 0 && end > off) {
                    lock();
                    if (have_seq && seq != exp_seq) {
                        int16_t gap = (int16_t)(seq - exp_seq);
                        if (gap > 0) {
                            s_st.lost += gap;
                        }
                    }
                    s_st.packets++;
                    s_st.bytes += (uint32_t)(end - off);
                    unlock();
                    have_seq = true;
                    exp_seq = seq + 1;
                    ring_push(pkt + off, end - off);
                    last_rtp = t;
                }
            }
        }
        if (r > 0 && FD_ISSET(c->tcp, &rs)) {
            char tmp[512];
            int n = recv(c->tcp, tmp, sizeof(tmp) - 1, 0);
            if (n <= 0) {
                ESP_LOGW(TAG, "RTSP connection closed by scanner");
                c->session[0] = 0;     /* nothing left to tear down */
                set_state(AUDIO_ERROR, "RTSP connection closed");
                break;
            }
            tmp[n] = 0;
            if (strncmp(tmp, "RTSP/1.0 ", 9) == 0 && atoi(tmp + 9) != 200) {
                ESP_LOGW(TAG, "keepalive reply: %.*s", 40, tmp);
            }
        }
        if (t - last_keep >= KEEPALIVE_MS) {
            last_keep = t;
            send_request(c, "GET_PARAMETER", uri, NULL);
            punch(c);
        }
        if (t - last_rtp >= RTP_SILENCE_MS) {
            ESP_LOGW(TAG, "no RTP for %d s", RTP_SILENCE_MS / 1000);
            set_state(AUDIO_ERROR, "no RTP packets received");
            break;
        }
    }
}

static void rtsp_task(void *arg)
{
    (void)arg;
    static rtsp_t c;
    for (;;) {
        xEventGroupSetBits(s_ev, EV_IDLE);
        xEventGroupWaitBits(s_ev, EV_START, pdTRUE, pdFALSE, portMAX_DELAY);
        xEventGroupClearBits(s_ev, EV_STOP | EV_IDLE);

        memset(&c, 0, sizeof(c));
        c.tcp = c.rtp = -1;
        app_settings_t cfg = settings_get();
        if (!cfg.scanner_ip[0]) {
            set_state(AUDIO_ERROR, "no scanner IP set");
            continue;
        }
        strlcpy(c.ip, cfg.scanner_ip, sizeof(c.ip));

        run_session(&c);

        s_want_play = false;
        bool was_error;
        lock();
        was_error = s_st.state == AUDIO_ERROR;
        if (!was_error) {
            s_st.state = AUDIO_STOPPING;
        }
        unlock();
        teardown(&c);          /* always, whatever ended the session */
        close_all(&c);
        ring_clear();
        if (!was_error) {
            set_state(AUDIO_IDLE, NULL);
        }
        xEventGroupClearBits(s_ev, EV_START);
    }
}

/* ------------------------------------------------------------------ API -- */

void audio_init(void)
{
    s_ev = xEventGroupCreate();
    s_lock = xSemaphoreCreateMutex();
    xEventGroupSetBits(s_ev, EV_IDLE);
    board_audio_set_volume(settings_get().local_volume);
    xTaskCreatePinnedToCore(rtsp_task, "rtsp", 6144, NULL, 6, NULL, 0);
    xTaskCreatePinnedToCore(playback_task, "playback", 4096, NULL, 7, NULL, 0);
}

void audio_start(void)
{
    if (xEventGroupGetBits(s_ev) & EV_IDLE) {
        xEventGroupClearBits(s_ev, EV_STOP);
        xEventGroupSetBits(s_ev, EV_START);
    }
}

void audio_stop(void)
{
    if (!(xEventGroupGetBits(s_ev) & EV_IDLE)) {
        xEventGroupSetBits(s_ev, EV_STOP);
    }
}

void audio_stop_blocking(int timeout_ms)
{
    audio_stop();
    xEventGroupWaitBits(s_ev, EV_IDLE, pdFALSE, pdFALSE, pdMS_TO_TICKS(timeout_ms));
}

void audio_network_down(void)
{
    audio_stop();
}

void audio_get_status(audio_status_t *out)
{
    lock();
    *out = s_st;
    unlock();
}

const char *audio_state_name(audio_state_t s)
{
    switch (s) {
    case AUDIO_IDLE: return "Stopped";
    case AUDIO_CONNECTING: return "Connecting";
    case AUDIO_BUFFERING: return "Buffering";
    case AUDIO_PLAYING: return "Playing";
    case AUDIO_STOPPING: return "Stopping";
    case AUDIO_ERROR: return "Error";
    }
    return "?";
}

void audio_set_local_volume(uint8_t percent)
{
    board_audio_set_volume(percent);
}
