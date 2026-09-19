/*
 * Wi-Fi station management with automatic reconnection.
 *
 * The P4 has no radio; esp_wifi_remote forwards the standard esp_wifi API to
 * the ESP32-C6 over ESP-Hosted (SDIO).
 */
#include <string.h>
#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"
#include "esp_wifi.h"
#include "esp_event.h"
#include "esp_netif.h"
#include "esp_timer.h"
#include "esp_log.h"
#include "lwip/ip4_addr.h"
#include "settings.h"
#include "net.h"

static const char *TAG = "net";

static SemaphoreHandle_t s_lock;
static net_status_t s_st;
static net_ap_t s_scan[NET_MAX_SCAN];
static int s_scan_n;
static uint32_t s_scan_seq;
static bool s_scan_busy;
static net_link_cb_t s_link_cb;
static esp_netif_t *s_netif;
static esp_timer_handle_t s_retry_timer;
static uint32_t s_retry_ms = 1000;
static uint32_t s_ip, s_mask;

static void lock(void) { xSemaphoreTake(s_lock, portMAX_DELAY); }
static void unlock(void) { xSemaphoreGive(s_lock); }

static void retry_cb(void *arg)
{
    (void)arg;
    lock();
    bool have = s_st.ssid[0] != 0;
    s_st.connecting = have;
    unlock();
    if (have) {
        esp_wifi_connect();
    }
}

static void schedule_retry(void)
{
    esp_timer_stop(s_retry_timer);
    esp_timer_start_once(s_retry_timer, (uint64_t)s_retry_ms * 1000);
    s_retry_ms = s_retry_ms < 16000 ? s_retry_ms * 2 : 30000;
}

static void on_event(void *arg, esp_event_base_t base, int32_t id, void *data)
{
    (void)arg;
    if (base == WIFI_EVENT) {
        switch (id) {
        case WIFI_EVENT_STA_START:
            lock();
            s_st.started = true;
            bool have = s_st.ssid[0] != 0;
            s_st.connecting = have;
            unlock();
            if (have) {
                esp_wifi_connect();
            }
            break;
        case WIFI_EVENT_STA_DISCONNECTED: {
            wifi_event_sta_disconnected_t *d = data;
            bool was_up;
            lock();
            was_up = s_st.connected;
            s_st.connected = false;
            s_st.ip[0] = 0;
            s_st.disconnects++;
            s_st.last_reason = d ? d->reason : -1;
            s_ip = s_mask = 0;
            unlock();
            ESP_LOGW(TAG, "disconnected (reason %d)", d ? d->reason : -1);
            if (was_up && s_link_cb) {
                s_link_cb(false);
            }
            schedule_retry();
            break;
        }
        case WIFI_EVENT_SCAN_DONE: {
            uint16_t n = NET_MAX_SCAN;
            wifi_ap_record_t *recs = calloc(NET_MAX_SCAN, sizeof(wifi_ap_record_t));
            int count = 0;
            if (recs && esp_wifi_scan_get_ap_records(&n, recs) == ESP_OK) {
                lock();
                for (int i = 0; i < n && count < NET_MAX_SCAN; i++) {
                    if (recs[i].ssid[0] == 0) {
                        continue;
                    }
                    bool dup = false;
                    for (int j = 0; j < count; j++) {
                        if (strcmp(s_scan[j].ssid, (const char *)recs[i].ssid) == 0) {
                            dup = true;
                            break;
                        }
                    }
                    if (dup) {
                        continue;
                    }
                    strlcpy(s_scan[count].ssid, (const char *)recs[i].ssid, sizeof(s_scan[count].ssid));
                    s_scan[count].rssi = recs[i].rssi;
                    s_scan[count].secure = recs[i].authmode != WIFI_AUTH_OPEN;
                    count++;
                }
                s_scan_n = count;
                s_scan_seq++;
                s_scan_busy = false;
                unlock();
            } else {
                esp_wifi_clear_ap_list();
                lock();
                s_scan_busy = false;
                s_scan_seq++;
                unlock();
            }
            free(recs);
            ESP_LOGI(TAG, "scan done: %d networks", count);
            break;
        }
        default:
            break;
        }
    } else if (base == IP_EVENT && id == IP_EVENT_STA_GOT_IP) {
        ip_event_got_ip_t *e = data;
        wifi_ap_record_t ap;
        lock();
        s_st.connected = true;
        s_st.connecting = false;
        snprintf(s_st.ip, sizeof(s_st.ip), IPSTR, IP2STR(&e->ip_info.ip));
        snprintf(s_st.netmask, sizeof(s_st.netmask), IPSTR, IP2STR(&e->ip_info.netmask));
        s_ip = lwip_ntohl(e->ip_info.ip.addr);
        s_mask = lwip_ntohl(e->ip_info.netmask.addr);
        unlock();
        if (esp_wifi_sta_get_ap_info(&ap) == ESP_OK) {
            lock();
            s_st.rssi = ap.rssi;
            unlock();
        }
        s_retry_ms = 1000;
        ESP_LOGI(TAG, "got IP " IPSTR, IP2STR(&e->ip_info.ip));
        if (s_link_cb) {
            s_link_cb(true);
        }
    }
}

void net_init(void)
{
    s_lock = xSemaphoreCreateMutex();
    ESP_ERROR_CHECK(esp_netif_init());
    ESP_ERROR_CHECK(esp_event_loop_create_default());
    s_netif = esp_netif_create_default_wifi_sta();
    esp_netif_set_hostname(s_netif, "sds200-panel");

    const esp_timer_create_args_t targs = {
        .callback = retry_cb,
        .name = "wifi_retry",
    };
    ESP_ERROR_CHECK(esp_timer_create(&targs, &s_retry_timer));

    wifi_init_config_t cfg = WIFI_INIT_CONFIG_DEFAULT();
    ESP_ERROR_CHECK(esp_wifi_init(&cfg));
    ESP_ERROR_CHECK(esp_event_handler_register(WIFI_EVENT, ESP_EVENT_ANY_ID, on_event, NULL));
    ESP_ERROR_CHECK(esp_event_handler_register(IP_EVENT, IP_EVENT_STA_GOT_IP, on_event, NULL));
    ESP_ERROR_CHECK(esp_wifi_set_storage(WIFI_STORAGE_RAM));
    ESP_ERROR_CHECK(esp_wifi_set_mode(WIFI_MODE_STA));

    app_settings_t s = settings_get();
    if (s.wifi_ssid[0]) {
        wifi_config_t wc = {0};
        strlcpy((char *)wc.sta.ssid, s.wifi_ssid, sizeof(wc.sta.ssid));
        strlcpy((char *)wc.sta.password, s.wifi_pass, sizeof(wc.sta.password));
        wc.sta.threshold.authmode = s.wifi_pass[0] ? WIFI_AUTH_WPA_PSK : WIFI_AUTH_OPEN;
        wc.sta.pmf_cfg.capable = true;
        esp_wifi_set_config(WIFI_IF_STA, &wc);
        strlcpy(s_st.ssid, s.wifi_ssid, sizeof(s_st.ssid));
    }
    ESP_ERROR_CHECK(esp_wifi_start());
    esp_wifi_set_ps(WIFI_PS_NONE);   /* lowest latency for RTP audio */
}

void net_set_link_cb(net_link_cb_t cb)
{
    s_link_cb = cb;
}

void net_connect(const char *ssid, const char *pass)
{
    settings_set_wifi(ssid, pass);
    wifi_config_t wc = {0};
    strlcpy((char *)wc.sta.ssid, ssid, sizeof(wc.sta.ssid));
    strlcpy((char *)wc.sta.password, pass ? pass : "", sizeof(wc.sta.password));
    wc.sta.threshold.authmode = (pass && pass[0]) ? WIFI_AUTH_WPA_PSK : WIFI_AUTH_OPEN;
    wc.sta.pmf_cfg.capable = true;

    esp_timer_stop(s_retry_timer);
    s_retry_ms = 1000;
    lock();
    strlcpy(s_st.ssid, ssid, sizeof(s_st.ssid));
    s_st.connecting = true;
    unlock();
    esp_wifi_disconnect();
    esp_wifi_set_config(WIFI_IF_STA, &wc);
    esp_wifi_connect();
}

void net_status(net_status_t *out)
{
    lock();
    *out = s_st;
    unlock();
    if (out->connected) {
        wifi_ap_record_t ap;
        if (esp_wifi_sta_get_ap_info(&ap) == ESP_OK) {
            out->rssi = ap.rssi;
        }
    }
}

bool net_scan_start(void)
{
    lock();
    if (s_scan_busy) {
        unlock();
        return true;
    }
    s_scan_busy = true;
    unlock();
    const wifi_scan_config_t sc = {
        .show_hidden = false,
        .scan_type = WIFI_SCAN_TYPE_ACTIVE,
    };
    esp_err_t err = esp_wifi_scan_start(&sc, false);
    if (err != ESP_OK) {
        ESP_LOGW(TAG, "scan start failed: %s", esp_err_to_name(err));
        lock();
        s_scan_busy = false;
        unlock();
        return false;
    }
    return true;
}

bool net_scan_busy(void)
{
    lock();
    bool b = s_scan_busy;
    unlock();
    return b;
}

int net_scan_results(net_ap_t *out, int max, uint32_t *seq)
{
    lock();
    int n = s_scan_n < max ? s_scan_n : max;
    memcpy(out, s_scan, n * sizeof(net_ap_t));
    if (seq) {
        *seq = s_scan_seq;
    }
    unlock();
    return n;
}

bool net_ipv4(uint32_t *ip, uint32_t *mask)
{
    lock();
    bool ok = s_st.connected;
    *ip = s_ip;
    *mask = s_mask;
    unlock();
    return ok;
}
