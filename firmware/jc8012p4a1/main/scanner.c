/*
 * SDS200 UDP control client.
 *
 * Protocol notes (verified against SDS200E firmware 1.23.15):
 *  - ASCII commands terminated by CR, replies CR-terminated, one exchange at
 *    a time with a 1.2 s timeout.
 *  - XML replies ("GSI,<XML>,\r<?xml ...") may span several datagrams and
 *    the "GSI,<XML>," marker may arrive as a datagram on its own.
 *  - STS escapes commas inside display text as TAB; bytes outside 0x20-0x7E
 *    are icon glyphs and are rendered as spaces.
 */
#include <string.h>
#include <stdlib.h>
#include <stdio.h>
#include <ctype.h>
#include <errno.h>
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "freertos/queue.h"
#include "freertos/semphr.h"
#include "esp_log.h"
#include "esp_timer.h"
#include "lwip/sockets.h"
#include "lwip/inet.h"
#include "app_config.h"
#include "settings.h"
#include "net.h"
#include "scanner.h"

static const char *TAG = "scanner";

typedef enum {
    SC_KEY,
    SC_FUNC,
    SC_STEP,
    SC_VOL,
    SC_SQL,
    SC_FQK_GET,
    SC_FQK_SET,
    SC_WF_ON,
    SC_WF_OFF,
    SC_DISCOVER,
    SC_SHUTDOWN,
} sc_type_t;

typedef struct {
    sc_type_t type;
    int value;
    uint8_t fqk[FQK_COUNT];
    SemaphoreHandle_t done;   /* optional completion signal */
} sc_cmd_t;

static QueueHandle_t s_q;
static SemaphoreHandle_t s_lock;
static scanner_state_t s_st;
static int s_sock = -1;
static struct sockaddr_in s_dest;
static bool s_dest_valid;
static volatile bool s_net_up;

#define RX_BUF_SIZE 16384
static char s_buf[RX_BUF_SIZE];
static int s_len;
static char s_dgram[8192];

static void lock(void) { xSemaphoreTake(s_lock, portMAX_DELAY); }
static void unlock(void) { xSemaphoreGive(s_lock); }
static int64_t now_ms(void) { return esp_timer_get_time() / 1000; }

/* ------------------------------------------------------------ transport -- */

static bool open_socket(void)
{
    if (s_sock >= 0) {
        return true;
    }
    s_sock = socket(AF_INET, SOCK_DGRAM, IPPROTO_UDP);
    if (s_sock < 0) {
        ESP_LOGE(TAG, "socket: errno %d", errno);
        return false;
    }
    int rcvbuf = 32768;
    setsockopt(s_sock, SOL_SOCKET, SO_RCVBUF, &rcvbuf, sizeof(rcvbuf));
    struct sockaddr_in local = {
        .sin_family = AF_INET,
        .sin_port = 0,
        .sin_addr.s_addr = htonl(INADDR_ANY),
    };
    bind(s_sock, (struct sockaddr *)&local, sizeof(local));
    return true;
}

static void set_rcv_timeout(int ms)
{
    if (ms < 1) {
        ms = 1;
    }
    struct timeval tv = {.tv_sec = ms / 1000, .tv_usec = (ms % 1000) * 1000};
    setsockopt(s_sock, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
}

static void drain(void)
{
    while (recv(s_sock, s_dgram, sizeof(s_dgram), MSG_DONTWAIT) > 0) {
    }
}

/* Finds the start of the reply for command name `exp` (line-anchored). */
static char *find_reply(const char *exp)
{
    size_t el = strlen(exp);
    char *p = s_buf;
    while (p && *p) {
        if (strncmp(p, exp, el) == 0 && (p[el] == ',' || p[el] == '\r' || p[el] == '\n' || p[el] == 0)) {
            return p;
        }
        if (strncmp(p, "ERR", 3) == 0 && (p[3] == '\r' || p[3] == '\n' || p[3] == 0)) {
            return p;
        }
        char *nl = strpbrk(p, "\r\n");
        p = nl ? nl + 1 : NULL;
    }
    return NULL;
}

static bool xml_complete(const char *start)
{
    const char *x = strstr(start, "<?xml");
    if (x) {
        x = strstr(x, "?>");
        if (!x) {
            return false;
        }
        x += 2;
    } else {
        x = strstr(start, ",<XML>,");
        if (!x) {
            return false;
        }
        x += 7;
    }
    /* root element name */
    const char *r = strchr(x, '<');
    while (r && (r[1] == '?' || r[1] == '!')) {
        r = strchr(r + 1, '<');
    }
    if (!r) {
        return false;
    }
    char root[40];
    int i = 0;
    for (r++; *r && i < (int)sizeof(root) - 1 && !isspace((unsigned char)*r) && *r != '>' && *r != '/'; r++) {
        root[i++] = *r;
    }
    root[i] = 0;
    if (!root[0]) {
        return false;
    }
    char close[48];
    snprintf(close, sizeof(close), "</%s>", root);
    if (strstr(x, close)) {
        return true;
    }
    /* numbered fragments: final fragment carries EOT="1" */
    const char *f = strstr(x, "<Footer");
    while (f) {
        const char *e = strchr(f, '>');
        const char *eot = strstr(f, "EOT=\"1\"");
        if (eot && e && eot < e) {
            return true;
        }
        f = strstr(f + 1, "<Footer");
    }
    return false;
}

/*
 * Sends `cmd` and waits for its reply. On success the reply starts at s_buf
 * and the return value is its length; -1 on timeout.
 */
static int transact(const char *cmd, int timeout_ms)
{
    struct sockaddr_in dest;
    bool valid;
    lock();
    dest = s_dest;
    valid = s_dest_valid;
    unlock();
    if (!valid || !open_socket()) {
        return -1;
    }
    drain();

    char exp[16];
    int i = 0;
    while (cmd[i] && cmd[i] != ',' && i < (int)sizeof(exp) - 1) {
        exp[i] = (char)toupper((unsigned char)cmd[i]);
        i++;
    }
    exp[i] = 0;

    char out[512];
    int n = snprintf(out, sizeof(out), "%s\r", cmd);
    if (n <= 0 || n >= (int)sizeof(out)) {
        return -1;
    }
    if (sendto(s_sock, out, n, 0, (struct sockaddr *)&dest, sizeof(dest)) < 0) {
        ESP_LOGD(TAG, "sendto failed: errno %d", errno);
        return -1;
    }

    s_len = 0;
    s_buf[0] = 0;
    const int64_t deadline = now_ms() + timeout_ms;
    for (;;) {
        int remaining = (int)(deadline - now_ms());
        if (remaining <= 0) {
            break;
        }
        set_rcv_timeout(remaining);
        struct sockaddr_in from;
        socklen_t fl = sizeof(from);
        int r = recvfrom(s_sock, s_dgram, sizeof(s_dgram), 0, (struct sockaddr *)&from, &fl);
        if (r < 0) {
            if (errno == EAGAIN || errno == EWOULDBLOCK || errno == EINTR) {
                continue;
            }
            break;
        }
        if (from.sin_addr.s_addr != dest.sin_addr.s_addr) {
            continue;
        }
        for (int k = 0; k < r && s_len < RX_BUF_SIZE - 1; k++) {
            if (s_dgram[k] != 0) {
                s_buf[s_len++] = s_dgram[k];
            }
        }
        s_buf[s_len] = 0;

        char *start = find_reply(exp);
        if (!start) {
            continue;
        }
        bool xml = strstr(start, ",<XML>,") != NULL || strstr(start, "<?xml") != NULL;
        if (xml && !xml_complete(start)) {
            continue;
        }
        if (start != s_buf) {
            s_len -= (int)(start - s_buf);
            memmove(s_buf, start, s_len + 1);
        }
        /* trim the trailing terminator of simple replies */
        if (!xml) {
            char *cr = strpbrk(s_buf, "\r\n");
            if (cr) {
                *cr = 0;
                s_len = (int)(cr - s_buf);
            }
        }
        return s_len;
    }
    return -1;
}

static bool reply_ok(const char *exp)
{
    char want[24];
    snprintf(want, sizeof(want), "%s,OK", exp);
    return strncmp(s_buf, want, strlen(want)) == 0;
}

/* --------------------------------------------------------------- parsing -- */

static void xml_decode(const char *src, size_t n, char *dst, size_t len)
{
    size_t o = 0;
    for (size_t i = 0; i < n && o + 1 < len;) {
        if (src[i] == '&') {
            static const struct { const char *e; char c; } ents[] = {
                {"&amp;", '&'}, {"&lt;", '<'}, {"&gt;", '>'}, {"&quot;", '"'}, {"&apos;", '\''},
            };
            bool hit = false;
            for (size_t k = 0; k < sizeof(ents) / sizeof(ents[0]); k++) {
                size_t el = strlen(ents[k].e);
                if (i + el <= n && strncmp(src + i, ents[k].e, el) == 0) {
                    dst[o++] = ents[k].c;
                    i += el;
                    hit = true;
                    break;
                }
            }
            if (hit) {
                continue;
            }
        }
        unsigned char c = (unsigned char)src[i++];
        dst[o++] = (c >= 0x20 && c < 0x7f) ? (char)c : ' ';
    }
    dst[o] = 0;
    /* trim */
    while (o > 0 && dst[o - 1] == ' ') {
        dst[--o] = 0;
    }
    size_t lead = 0;
    while (dst[lead] == ' ') {
        lead++;
    }
    if (lead) {
        memmove(dst, dst + lead, o - lead + 1);
    }
}

/* Returns a pointer to the first "<tag" element (followed by space, / or >). */
static const char *xml_find(const char *xml, const char *tag)
{
    size_t tl = strlen(tag);
    const char *p = xml;
    while ((p = strchr(p, '<')) != NULL) {
        if (strncmp(p + 1, tag, tl) == 0) {
            char c = p[1 + tl];
            if (c == ' ' || c == '/' || c == '>' || c == '\t' || c == '\r' || c == '\n') {
                return p;
            }
        }
        p++;
    }
    return NULL;
}

static bool xml_attr(const char *elem, const char *attr, char *out, size_t len)
{
    out[0] = 0;
    if (!elem) {
        return false;
    }
    const char *end = strchr(elem, '>');
    if (!end) {
        end = elem + strlen(elem);
    }
    size_t al = strlen(attr);
    for (const char *p = elem + 1; p < end; p++) {
        if ((p[-1] == ' ' || p[-1] == '\t' || p[-1] == '\r' || p[-1] == '\n') &&
            strncmp(p, attr, al) == 0 && p[al] == '=' && p[al + 1] == '"') {
            const char *v = p + al + 2;
            const char *q = memchr(v, '"', end - v);
            if (!q) {
                return false;
            }
            xml_decode(v, q - v, out, len);
            return true;
        }
    }
    return false;
}

static int attr_int(const char *elem, const char *attr, int dflt)
{
    char tmp[16];
    if (!xml_attr(elem, attr, tmp, sizeof(tmp)) || !tmp[0]) {
        return dflt;
    }
    char *e;
    long v = strtol(tmp, &e, 10);
    return e == tmp ? dflt : (int)v;
}

static void parse_gsi(void)
{
    const char *x = strstr(s_buf, "<?xml");
    if (!x) {
        x = s_buf;
    }
    const char *root = xml_find(x, "ScannerInfo");
    const char *mon = xml_find(x, "MonitorList");
    const char *sys = xml_find(x, "System");
    const char *dept = xml_find(x, "Department");
    const char *site = xml_find(x, "Site");
    const char *sitef = xml_find(x, "SiteFrequency");
    const char *prop = xml_find(x, "Property");
    const char *uid = xml_find(x, "UnitID");

    static const char *const chan_tags[] = {
        "ConvFrequency", "TGID", "SrchFrequency", "CcHitsChannel", "ToneOutChannel", "WxChannel",
    };
    const char *ch = NULL;
    const char *ch_tag = "";
    for (size_t i = 0; i < sizeof(chan_tags) / sizeof(chan_tags[0]); i++) {
        const char *e = xml_find(x, chan_tags[i]);
        if (e && (!ch || e < ch)) {
            ch = e;
            ch_tag = chan_tags[i];
        }
    }

    scanner_state_t *s = &s_st;
    char tmp[64];
    lock();
    xml_attr(root, "Mode", s->mode, sizeof(s->mode));
    xml_attr(root, "V_Screen", s->v_screen, sizeof(s->v_screen));
    xml_attr(mon, "Name", s->monitor_list, sizeof(s->monitor_list));
    xml_attr(sys, "Name", s->system, sizeof(s->system));
    xml_attr(sys, "Hold", s->system_hold, sizeof(s->system_hold));
    xml_attr(dept, "Name", s->department, sizeof(s->department));
    xml_attr(dept, "Hold", s->department_hold, sizeof(s->department_hold));
    xml_attr(site, "Name", s->site, sizeof(s->site));
    xml_attr(site, "Hold", s->site_hold, sizeof(s->site_hold));
    strlcpy(s->channel_tag, ch_tag, sizeof(s->channel_tag));
    xml_attr(ch, "Name", s->channel, sizeof(s->channel));
    xml_attr(ch, "Hold", s->channel_hold, sizeof(s->channel_hold));
    s->channel_index = attr_int(ch, "Index", -1);
    {
        char f[8] = "";
        xml_attr(prop, "F", f, sizeof(f));
        s->func_on = strcmp(f, "On") == 0;
        s->popup = strstr(x, "<PopupScreen") != NULL;
    }
    xml_attr(ch, "SvcType", s->service_type, sizeof(s->service_type));

    if (!xml_attr(ch, "Freq", s->frequency, sizeof(s->frequency)) || !s->frequency[0]) {
        if (!xml_attr(sitef, "Freq", s->frequency, sizeof(s->frequency)) || !s->frequency[0]) {
            xml_attr(site, "Freq", s->frequency, sizeof(s->frequency));
        }
    }
    if (!xml_attr(ch, "TGID", s->tgid, sizeof(s->tgid)) || !s->tgid[0]) {
        xml_attr(ch, "Id", s->tgid, sizeof(s->tgid));
    }
    if (!xml_attr(ch, "U_Id", s->unit_id, sizeof(s->unit_id)) || !s->unit_id[0]) {
        if (!xml_attr(uid, "U_Id", s->unit_id, sizeof(s->unit_id)) || !s->unit_id[0]) {
            xml_attr(prop, "U_Id", s->unit_id, sizeof(s->unit_id));
        }
    }
    if (!xml_attr(ch, "Mod", s->modulation, sizeof(s->modulation)) || !s->modulation[0]) {
        if (!xml_attr(site, "Mod", s->modulation, sizeof(s->modulation)) || !s->modulation[0]) {
            xml_attr(prop, "P25Status", s->modulation, sizeof(s->modulation));
        }
    }
    s->volume = attr_int(prop, "VOL", -1);
    s->squelch = attr_int(prop, "SQL", -1);
    s->signal = attr_int(prop, "Sig", -1);
    s->rssi = attr_int(prop, "Rssi", 0);
    xml_attr(prop, "Mute", s->mute, sizeof(s->mute));
    if (!xml_attr(prop, "Att", s->att, sizeof(s->att))) {
        xml_attr(prop, "ATT", s->att, sizeof(s->att));
    }
    xml_attr(prop, "Rec", s->rec, sizeof(s->rec));
    xml_attr(prop, "P25Status", s->p25_status, sizeof(s->p25_status));
    (void)tmp;
    s->gsi_seq++;
    unlock();
}

static void sts_copy(char *dst, const char *src, size_t n)
{
    size_t o = 0;
    for (size_t i = 0; i < n && o < STS_COLS; i++) {
        unsigned char c = (unsigned char)src[i];
        if (c == '\t') {
            c = ',';
        } else if (c < 0x20 || c > 0x7e) {
            c = ' ';
        }
        dst[o++] = (char)c;
    }
    while (o < STS_COLS) {
        dst[o++] = ' ';
    }
    dst[STS_COLS] = 0;
}

static bool parse_sts(void)
{
    if (strncmp(s_buf, "STS,", 4) != 0) {
        return false;
    }
    /* split into fields without touching the buffer */
    const char *f[2 + 2 * STS_MAX_LINES + 16];
    size_t fl[sizeof(f) / sizeof(f[0])];
    int nf = 0;
    const char *p = s_buf + 4;
    const char *end = s_buf + s_len;
    while (p <= end && nf < (int)(sizeof(f) / sizeof(f[0]))) {
        const char *c = memchr(p, ',', end - p);
        if (!c) {
            c = end;
        }
        f[nf] = p;
        fl[nf] = c - p;
        nf++;
        p = c + 1;
    }
    if (nf < 1) {
        return false;
    }
    int lines = (int)fl[0];
    if (lines <= 0 || lines > STS_MAX_LINES || nf < 1 + 2 * lines) {
        return false;
    }
    sts_line_t tmp[STS_MAX_LINES];
    for (int i = 0; i < lines; i++) {
        tmp[i].large = f[0][i] == '1';
        sts_copy(tmp[i].text, f[1 + 2 * i], fl[1 + 2 * i]);
        sts_copy(tmp[i].mode, f[2 + 2 * i], fl[2 + 2 * i]);
    }
    lock();
    bool changed = s_st.sts_count != lines || memcmp(s_st.sts, tmp, lines * sizeof(sts_line_t)) != 0;
    if (changed) {
        memcpy(s_st.sts, tmp, lines * sizeof(sts_line_t));
        s_st.sts_count = lines;
        s_st.sts_seq++;
    }
    unlock();
    return true;
}

static int hexval(char c)
{
    if (c >= '0' && c <= '9') {
        return c - '0';
    }
    c = (char)toupper((unsigned char)c);
    if (c >= 'A' && c <= 'F') {
        return c - 'A' + 10;
    }
    return -1;
}

static bool parse_gwf(void)
{
    if (strncmp(s_buf, "GWF,", 4) != 0) {
        return false;
    }
    uint8_t bins[WF_BINS];
    int n = 0;
    const char *p = s_buf + 4;
    while (*p && n < WF_BINS) {
        if (*p == ',' || *p == ' ') {
            p++;
            continue;
        }
        int h = hexval(p[0]);
        int l = hexval(p[1]);
        if (h < 0) {
            return false;
        }
        if (l < 0) {           /* single digit field */
            bins[n++] = (uint8_t)h;
            p += 1;
        } else {
            bins[n++] = (uint8_t)(h * 16 + l);
            p += 2;
        }
    }
    if (n != WF_BINS) {
        return false;
    }
    lock();
    memcpy(s_st.wf_bins, bins, sizeof(bins));
    s_st.wf_seq++;
    unlock();
    return true;
}

static bool parse_fqk(void)
{
    if (strncmp(s_buf, "FQK,", 4) != 0) {
        return false;
    }
    uint8_t v[FQK_COUNT];
    int n = 0;
    for (const char *p = s_buf + 4; *p && n < FQK_COUNT; p++) {
        if (*p >= '0' && *p <= '2') {
            v[n++] = (uint8_t)(*p - '0');
        }
    }
    if (n != FQK_COUNT) {
        return false;
    }
    lock();
    memcpy(s_st.fqk, v, sizeof(v));
    s_st.fqk_valid = true;
    s_st.fqk_seq++;
    unlock();
    return true;
}

/* ------------------------------------------------------------ discovery -- */

static void do_discover(void)
{
    uint32_t ip, mask;
    lock();
    s_st.discovering = true;
    s_st.discovered_ip[0] = 0;
    unlock();

    if (!net_ipv4(&ip, &mask) || !open_socket()) {
        goto done;
    }
    drain();
    uint32_t base = ip & 0xFFFFFF00u;
    ESP_LOGI(TAG, "discovery: probing %u.%u.%u.1-254", (unsigned)(base >> 24), (unsigned)((base >> 16) & 255),
             (unsigned)((base >> 8) & 255));
    for (uint32_t h = 1; h < 255; h++) {
        uint32_t a = base | h;
        if (a == ip) {
            continue;
        }
        struct sockaddr_in d = {
            .sin_family = AF_INET,
            .sin_port = htons(SCANNER_UDP_PORT),
            .sin_addr.s_addr = htonl(a),
        };
        sendto(s_sock, "MDL\r", 4, 0, (struct sockaddr *)&d, sizeof(d));
        if ((h & 15) == 0) {
            vTaskDelay(pdMS_TO_TICKS(10));
        }
    }
    const int64_t deadline = now_ms() + 2500;
    while (now_ms() < deadline) {
        set_rcv_timeout((int)(deadline - now_ms()));
        struct sockaddr_in from;
        socklen_t fl = sizeof(from);
        int r = recvfrom(s_sock, s_dgram, sizeof(s_dgram) - 1, 0, (struct sockaddr *)&from, &fl);
        if (r <= 0) {
            continue;
        }
        s_dgram[r] = 0;
        if (strncmp(s_dgram, "MDL,", 4) == 0) {
            char ipstr[16];
            inet_ntoa_r(from.sin_addr, ipstr, sizeof(ipstr));
            ESP_LOGI(TAG, "discovery: %s answered %.*s", ipstr, 24, s_dgram);
            lock();
            strlcpy(s_st.discovered_ip, ipstr, sizeof(s_st.discovered_ip));
            unlock();
            if (strstr(s_dgram, "SDS")) {
                break;
            }
        }
    }
done:
    lock();
    s_st.discovering = false;
    s_st.discover_seq++;
    char found[16];
    strlcpy(found, s_st.discovered_ip, sizeof(found));
    unlock();
    if (found[0]) {
        scanner_set_ip(found);
    }
}

/* ----------------------------------------------------------------- task -- */

static void set_error(const char *msg)
{
    lock();
    strlcpy(s_st.last_error, msg, sizeof(s_st.last_error));
    unlock();
}

static int s_fail_count;

static void note_result(bool ok)
{
    bool was;
    lock();
    was = s_st.online;
    if (ok) {
        s_fail_count = 0;
        s_st.online = true;
    } else if (++s_fail_count >= 3) {
        s_st.online = false;
    }
    bool now_on = s_st.online;
    unlock();
    if (was != now_on) {
        ESP_LOGI(TAG, "scanner %s", now_on ? "online" : "offline");
    }
}

static void wf_stop_sequence(void)
{
    transact("GWF,1,OFF", 900);
    transact("PWF,1,OFF", 900);
    transact("JPM,SCN_MODE", 2000);
    lock();
    s_st.wf_active = false;
    unlock();
}

static bool wf_start_sequence(void)
{
    if (transact("JPM,WF_MODE", 2000) < 0) {
        set_error("JPM,WF_MODE: no reply");
        return false;
    }
    if (transact("PWF,1,ON", 1500) < 0) {
        transact("JPM,SCN_MODE", 2000);
        set_error("PWF,1,ON: no reply");
        return false;
    }
    lock();
    s_st.wf_active = true;
    unlock();
    return true;
}

static void scanner_task(void *arg)
{
    (void)arg;
    int64_t next_gsi = 0, next_sts = 0, next_gwf = 0, next_mdl = 0;
    bool need_fqk = true;
    int wf_miss = 0;

    for (;;) {
        int64_t t = now_ms();
        bool wf;
        lock();
        wf = s_st.wf_active;
        unlock();
        int64_t due = next_gsi < next_sts ? next_gsi : next_sts;
        if (wf && next_gwf < due) {
            due = next_gwf;
        }
        int wait = (int)(due - t);
        if (wait < 0) {
            wait = 0;
        }
        if (wait > 500) {
            wait = 500;
        }

        sc_cmd_t cmd;
        if (xQueueReceive(s_q, &cmd, pdMS_TO_TICKS(wait)) == pdTRUE) {
            char line[400];
            switch (cmd.type) {
            case SC_KEY:
                snprintf(line, sizeof(line), "KEY,%c,P", (char)cmd.value);
                if (transact(line, SCANNER_TIMEOUT_MS) >= 0 && reply_ok("KEY")) {
                    note_result(true);
                } else {
                    set_error("KEY not acknowledged");
                }
                /* confirm with a fresh display read now and again shortly after */
                next_sts = now_ms();
                next_gsi = now_ms() + 200;
                break;
            case SC_FUNC: {
                /* wait out transient popups: a key press during one only dismisses it */
                bool popup = true, fon = false;
                for (int i = 0; i < 15 && popup; i++) {
                    if (transact("GSI", 2500) < 0) {
                        break;
                    }
                    parse_gsi();
                    lock();
                    popup = s_st.popup;
                    fon = s_st.func_on;
                    unlock();
                    if (popup) {
                        vTaskDelay(pdMS_TO_TICKS(200));
                    }
                }
                bool ok = true;
                if (!fon) {
                    ok = transact("KEY,F,P", SCANNER_TIMEOUT_MS) >= 0 && reply_ok("KEY");
                    vTaskDelay(pdMS_TO_TICKS(250));
                }
                snprintf(line, sizeof(line), "KEY,%c,P", (char)cmd.value);
                ok = ok && transact(line, SCANNER_TIMEOUT_MS) >= 0 && reply_ok("KEY");
                if (ok) {
                    note_result(true);
                } else {
                    set_error("FUNC key not acknowledged");
                }
                next_sts = now_ms();
                next_gsi = now_ms() + 200;
                break;
            }
            case SC_STEP: {
                char tag[20];
                int idx;
                lock();
                strlcpy(tag, s_st.channel_tag, sizeof(tag));
                idx = s_st.channel_index;
                unlock();
                const char *tkw = NULL;
                if (!strcmp(tag, "ConvFrequency")) {
                    tkw = "CFREQ";
                } else if (!strcmp(tag, "TGID")) {
                    tkw = "TGID";
                } else if (!strcmp(tag, "CcHitsChannel")) {
                    tkw = "CCHIT";
                } else if (!strcmp(tag, "ToneOutChannel")) {
                    tkw = "FTO";
                } else if (!strcmp(tag, "WxChannel")) {
                    tkw = "WX";
                }
                const char *verb = cmd.value > 0 ? "NXT" : "PRV";
                bool ok = false;
                if (tkw && idx >= 0) {
                    snprintf(line, sizeof(line), "%s,%s,%d,,1", verb, tkw, idx);
                    ok = transact(line, SCANNER_TIMEOUT_MS) >= 0 && reply_ok(verb);
                }
                if (!ok) {
                    /* no channel index in this mode (search/Close Call): use the rotary */
                    snprintf(line, sizeof(line), "KEY,%c,P", cmd.value > 0 ? '>' : '<');
                    ok = transact(line, SCANNER_TIMEOUT_MS) >= 0 && reply_ok("KEY");
                }
                if (ok) {
                    note_result(true);
                } else {
                    set_error("Next/previous not acknowledged");
                }
                next_sts = now_ms();
                next_gsi = now_ms() + 200;
                break;
            }
            case SC_VOL:
            case SC_SQL:
                snprintf(line, sizeof(line), "%s,%d", cmd.type == SC_VOL ? "VOL" : "SQL", cmd.value);
                if (transact(line, SCANNER_TIMEOUT_MS) < 0 || !reply_ok(cmd.type == SC_VOL ? "VOL" : "SQL")) {
                    set_error(cmd.type == SC_VOL ? "VOL not acknowledged" : "SQL not acknowledged");
                } else {
                    lock();
                    if (cmd.type == SC_VOL) {
                        s_st.volume = cmd.value;
                    } else {
                        s_st.squelch = cmd.value;
                    }
                    unlock();
                }
                next_gsi = now_ms() + 100;
                break;
            case SC_FQK_GET:
                need_fqk = true;
                break;
            case SC_FQK_SET: {
                int o = snprintf(line, sizeof(line), "FQK");
                for (int i = 0; i < FQK_COUNT; i++) {
                    o += snprintf(line + o, sizeof(line) - o, ",%u", cmd.fqk[i]);
                }
                if (transact(line, 2000) >= 0 && reply_ok("FQK")) {
                    lock();
                    memcpy(s_st.fqk, cmd.fqk, FQK_COUNT);
                    s_st.fqk_valid = true;
                    s_st.fqk_seq++;
                    unlock();
                } else {
                    set_error("FQK write failed");
                }
                need_fqk = true;
                break;
            }
            case SC_WF_ON:
                if (!wf && wf_start_sequence()) {
                    wf_miss = 0;
                    next_gwf = now_ms();
                }
                break;
            case SC_WF_OFF:
                if (wf) {
                    wf_stop_sequence();
                }
                break;
            case SC_DISCOVER:
                do_discover();
                next_gsi = next_sts = now_ms();
                need_fqk = true;
                break;
            case SC_SHUTDOWN:
                if (wf) {
                    wf_stop_sequence();
                }
                break;
            }
            if (cmd.done) {
                xSemaphoreGive(cmd.done);
            }
            continue;
        }

        if (!s_net_up || !s_dest_valid) {
            lock();
            s_st.online = false;
            unlock();
            next_gsi = next_sts = now_ms() + 500;
            continue;
        }

        t = now_ms();
        bool online;
        lock();
        online = s_st.online;
        wf = s_st.wf_active;
        unlock();

        if (online && t >= next_mdl) {
            if (transact("MDL", SCANNER_TIMEOUT_MS) > 0 && strncmp(s_buf, "MDL,", 4) == 0) {
                lock();
                strlcpy(s_st.model, s_buf + 4, sizeof(s_st.model));
                unlock();
                next_mdl = t + 600000;
            } else {
                next_mdl = t + 10000;
            }
        }
        if (online && need_fqk) {
            if (transact("FQK", 1500) > 0 && parse_fqk()) {
                need_fqk = false;
            }
        }
        if (wf && t >= next_gwf) {
            next_gwf = t + 250;
            if (transact("GWF,1,ON", 1100) > 0 && parse_gwf()) {
                wf_miss = 0;
                note_result(true);
            } else if (++wf_miss >= 8) {
                ESP_LOGW(TAG, "waterfall: repeated GWF failures, leaving waterfall mode");
                set_error("waterfall stopped: no GWF data");
                wf_stop_sequence();
            }
            continue;
        }
        if (t >= next_sts) {
            next_sts = t + (wf ? 1000 : (online ? 333 : 2000));
            if (transact("STS", SCANNER_TIMEOUT_MS) > 0 && parse_sts()) {
                note_result(true);
            } else {
                note_result(false);
            }
            continue;
        }
        if (t >= next_gsi) {
            next_gsi = t + (wf ? 1000 : (online ? 500 : 2000));
            if (transact("GSI", SCANNER_TIMEOUT_MS) > 0 && strstr(s_buf, "ScannerInfo")) {
                parse_gsi();
                note_result(true);
            } else {
                note_result(false);
            }
        }
    }
}

/* ------------------------------------------------------------------ API -- */

void scanner_set_ip(const char *ip)
{
    struct in_addr a;
    bool ok = ip && inet_aton(ip, &a);
    lock();
    if (ok) {
        memset(&s_dest, 0, sizeof(s_dest));
        s_dest.sin_family = AF_INET;
        s_dest.sin_port = htons(SCANNER_UDP_PORT);
        s_dest.sin_addr = a;
        strlcpy(s_st.ip, ip, sizeof(s_st.ip));
        s_st.model[0] = 0;
    } else {
        s_st.ip[0] = 0;
    }
    s_dest_valid = ok;
    s_st.online = false;
    s_fail_count = 0;
    unlock();
    settings_set_scanner_ip(ok ? ip : "");
}

void scanner_init(void)
{
    s_lock = xSemaphoreCreateMutex();
    s_q = xQueueCreate(8, sizeof(sc_cmd_t));
    memset(&s_st, 0, sizeof(s_st));
    s_st.volume = s_st.squelch = s_st.signal = -1;

    app_settings_t cfg = settings_get();
    struct in_addr a;
    if (cfg.scanner_ip[0] && inet_aton(cfg.scanner_ip, &a)) {
        s_dest.sin_family = AF_INET;
        s_dest.sin_port = htons(SCANNER_UDP_PORT);
        s_dest.sin_addr = a;
        s_dest_valid = true;
        strlcpy(s_st.ip, cfg.scanner_ip, sizeof(s_st.ip));
    }
    xTaskCreatePinnedToCore(scanner_task, "scanner", 8192, NULL, 5, NULL, 0);
}

void scanner_get_state(scanner_state_t *out)
{
    lock();
    memcpy(out, &s_st, sizeof(*out));
    unlock();
}

static bool enqueue(sc_type_t type, int value, const uint8_t *fqk)
{
    sc_cmd_t c = {.type = type, .value = value, .done = NULL};
    if (fqk) {
        memcpy(c.fqk, fqk, FQK_COUNT);
    }
    return xQueueSend(s_q, &c, 0) == pdTRUE;
}

/* T, R and Q are not SDS200 keys (verified on fw 1.23.15: the radio treats
 * them as digit entry). Service Types = FUNC+Z; squelch-knob push = MENU. */
static const char s_valid_keys[] = "MFL0123456789.E><^VYABCZ";

bool scanner_func(char code)
{
    if (!code || code == 'F' || !strchr(s_valid_keys, code)) {
        return false;
    }
    return enqueue(SC_FUNC, code, NULL);
}

bool scanner_step(int dir)
{
    return enqueue(SC_STEP, dir > 0 ? 1 : -1, NULL);
}

bool scanner_key(char code)
{
    if (!code || !strchr(s_valid_keys, code)) {
        return false;
    }
    return enqueue(SC_KEY, code, NULL);
}

bool scanner_set_volume(int v)
{
    if (v < 0 || v > 29) {
        return false;
    }
    return enqueue(SC_VOL, v, NULL);
}

bool scanner_set_squelch(int v)
{
    if (v < 0 || v > 19) {
        return false;
    }
    return enqueue(SC_SQL, v, NULL);
}

bool scanner_fqk_refresh(void) { return enqueue(SC_FQK_GET, 0, NULL); }

bool scanner_fqk_set(const uint8_t states[FQK_COUNT])
{
    for (int i = 0; i < FQK_COUNT; i++) {
        if (states[i] > 2) {
            return false;
        }
    }
    return enqueue(SC_FQK_SET, 0, states);
}

bool scanner_waterfall(bool on) { return enqueue(on ? SC_WF_ON : SC_WF_OFF, 0, NULL); }
bool scanner_discover(void) { return enqueue(SC_DISCOVER, 0, NULL); }

void scanner_set_network(bool up)
{
    s_net_up = up;
}

void scanner_shutdown(void)
{
    /* Kept for the lifetime of the program: the scanner task may signal it
     * after a timeout here, so it must never be freed. */
    static SemaphoreHandle_t done;
    if (!done) {
        done = xSemaphoreCreateBinary();
    }
    xSemaphoreTake(done, 0);
    sc_cmd_t c = {.type = SC_SHUTDOWN, .done = done};
    if (xQueueSend(s_q, &c, pdMS_TO_TICKS(500)) == pdTRUE) {
        xSemaphoreTake(done, pdMS_TO_TICKS(6000));
    }
}
