/*
 * Persistent settings backed by NVS. All setters write through immediately.
 */
#include <string.h>
#include "freertos/FreeRTOS.h"
#include "freertos/semphr.h"
#include "nvs_flash.h"
#include "nvs.h"
#include "esp_log.h"
#include "sdkconfig.h"
#include "settings.h"

static const char *TAG = "settings";
static const char *NS = "sds200";
static app_settings_t s_cfg;
static SemaphoreHandle_t s_lock;

static void load_str(nvs_handle_t h, const char *key, char *dst, size_t len, const char *dflt)
{
    size_t l = len;
    if (nvs_get_str(h, key, dst, &l) != ESP_OK) {
        strlcpy(dst, dflt, len);
    }
}

static uint8_t load_u8(nvs_handle_t h, const char *key, uint8_t dflt)
{
    uint8_t v;
    return nvs_get_u8(h, key, &v) == ESP_OK ? v : dflt;
}

void settings_init(void)
{
    esp_err_t err = nvs_flash_init();
    if (err == ESP_ERR_NVS_NO_FREE_PAGES || err == ESP_ERR_NVS_NEW_VERSION_FOUND) {
        ESP_LOGW(TAG, "NVS needs erasing (%s)", esp_err_to_name(err));
        ESP_ERROR_CHECK(nvs_flash_erase());
        err = nvs_flash_init();
    }
    ESP_ERROR_CHECK(err);
    s_lock = xSemaphoreCreateMutex();

    memset(&s_cfg, 0, sizeof(s_cfg));
    nvs_handle_t h;
    bool opened = nvs_open(NS, NVS_READONLY, &h) == ESP_OK;
    if (opened) {
        load_str(h, "ssid", s_cfg.wifi_ssid, sizeof(s_cfg.wifi_ssid), CONFIG_SDS_DEFAULT_WIFI_SSID);
        load_str(h, "pass", s_cfg.wifi_pass, sizeof(s_cfg.wifi_pass), CONFIG_SDS_DEFAULT_WIFI_PASSWORD);
        load_str(h, "ip", s_cfg.scanner_ip, sizeof(s_cfg.scanner_ip), CONFIG_SDS_DEFAULT_SCANNER_IP);
        s_cfg.backlight = load_u8(h, "bl", 80);
        s_cfg.panel_rev = load_u8(h, "rev", CONFIG_SDS_PANEL_REV_DEFAULT);
        s_cfg.flip = load_u8(h, "flip", 0) != 0;
        s_cfg.local_volume = load_u8(h, "lvol", 70);
        nvs_close(h);
    } else {
        strlcpy(s_cfg.wifi_ssid, CONFIG_SDS_DEFAULT_WIFI_SSID, sizeof(s_cfg.wifi_ssid));
        strlcpy(s_cfg.wifi_pass, CONFIG_SDS_DEFAULT_WIFI_PASSWORD, sizeof(s_cfg.wifi_pass));
        strlcpy(s_cfg.scanner_ip, CONFIG_SDS_DEFAULT_SCANNER_IP, sizeof(s_cfg.scanner_ip));
        s_cfg.backlight = 80;
        s_cfg.panel_rev = CONFIG_SDS_PANEL_REV_DEFAULT;
        s_cfg.local_volume = 70;
    }
    if (s_cfg.panel_rev != 1 && s_cfg.panel_rev != 2) {
        s_cfg.panel_rev = CONFIG_SDS_PANEL_REV_DEFAULT;
    }
    if (s_cfg.backlight < 5 || s_cfg.backlight > 100) {
        s_cfg.backlight = 80;
    }
    if (s_cfg.local_volume > 100) {
        s_cfg.local_volume = 70;
    }
    ESP_LOGI(TAG, "ssid='%s' scanner='%s' panel=V%u bl=%u%%", s_cfg.wifi_ssid, s_cfg.scanner_ip,
             s_cfg.panel_rev, s_cfg.backlight);
}

app_settings_t settings_get(void)
{
    app_settings_t c;
    xSemaphoreTake(s_lock, portMAX_DELAY);
    c = s_cfg;
    xSemaphoreGive(s_lock);
    return c;
}

static void commit_str(const char *key, const char *val)
{
    nvs_handle_t h;
    if (nvs_open(NS, NVS_READWRITE, &h) == ESP_OK) {
        nvs_set_str(h, key, val);
        nvs_commit(h);
        nvs_close(h);
    }
}

static void commit_u8(const char *key, uint8_t val)
{
    nvs_handle_t h;
    if (nvs_open(NS, NVS_READWRITE, &h) == ESP_OK) {
        nvs_set_u8(h, key, val);
        nvs_commit(h);
        nvs_close(h);
    }
}

void settings_set_wifi(const char *ssid, const char *pass)
{
    xSemaphoreTake(s_lock, portMAX_DELAY);
    strlcpy(s_cfg.wifi_ssid, ssid ? ssid : "", sizeof(s_cfg.wifi_ssid));
    strlcpy(s_cfg.wifi_pass, pass ? pass : "", sizeof(s_cfg.wifi_pass));
    xSemaphoreGive(s_lock);
    commit_str("ssid", s_cfg.wifi_ssid);
    commit_str("pass", s_cfg.wifi_pass);
}

void settings_set_scanner_ip(const char *ip)
{
    xSemaphoreTake(s_lock, portMAX_DELAY);
    strlcpy(s_cfg.scanner_ip, ip ? ip : "", sizeof(s_cfg.scanner_ip));
    xSemaphoreGive(s_lock);
    commit_str("ip", s_cfg.scanner_ip);
}

#define U8_SETTER(fn, field, key, type)                 \
    void fn(type v)                                     \
    {                                                   \
        xSemaphoreTake(s_lock, portMAX_DELAY);          \
        s_cfg.field = v;                                \
        xSemaphoreGive(s_lock);                         \
        commit_u8(key, (uint8_t)v);                     \
    }

U8_SETTER(settings_set_backlight, backlight, "bl", uint8_t)
U8_SETTER(settings_set_panel_rev, panel_rev, "rev", uint8_t)
U8_SETTER(settings_set_flip, flip, "flip", bool)
U8_SETTER(settings_set_local_volume, local_volume, "lvol", uint8_t)
