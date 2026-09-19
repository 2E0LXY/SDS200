/*
 * SDS200 Panel - standalone touchscreen remote for the Uniden SDS200/SDS200E.
 * Target: Guition JC8012P4A1 (ESP32-P4, 10.1" 800x1280 MIPI-DSI, ESP32-C6 Wi-Fi).
 *
 * Tasks:
 *   scanner   - UDP control (GSI/STS/GWF polling, queued commands)   core 0
 *   rtsp      - RTSP session + RTP receive                          core 0
 *   playback  - jitter buffer -> ES8311 over I2S                    core 0
 *   LVGL      - UI (esp_lvgl_port task)                             core 1
 *   httpd     - HTTP API (optional)
 */
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "esp_log.h"
#include "esp_system.h"
#include "esp_app_desc.h"
#include "esp_lvgl_port.h"
#include "sdkconfig.h"
#include "settings.h"
#include "board.h"
#include "net.h"
#include "scanner.h"
#include "audio.h"
#include "ui.h"
#include "web.h"
#include "app.h"

static const char *TAG = "main";

static void on_link(bool up)
{
    /* Runs in the event loop task: keep it short and non-blocking. */
    scanner_set_network(up);
    if (!up) {
        audio_network_down();
    }
}

static void reboot_task(void *arg)
{
    (void)arg;
    ESP_LOGI(TAG, "reboot requested: stopping audio and waterfall first");
    audio_stop_blocking(5000);   /* RTSP TEARDOWN */
    scanner_shutdown();          /* leave waterfall mode if active */
    vTaskDelay(pdMS_TO_TICKS(200));
    esp_restart();
}

void app_reboot(void)
{
    static bool started;
    if (!started) {
        started = true;
        xTaskCreate(reboot_task, "reboot", 4096, NULL, 10, NULL);
    }
}

void app_main(void)
{
    const esp_app_desc_t *d = esp_app_get_description();
    ESP_LOGI(TAG, "SDS200 Panel %s starting", d->version);

    settings_init();
    app_settings_t cfg = settings_get();

    ESP_ERROR_CHECK(board_i2c_init());
    ESP_ERROR_CHECK(board_backlight_init());
    lv_display_t *disp = board_display_init(cfg.panel_rev, cfg.flip);
    if (board_audio_init() != ESP_OK) {
        ESP_LOGE(TAG, "audio codec initialisation failed; live audio will not play");
    }

    scanner_init();
    audio_init();

    if (lvgl_port_lock(0)) {
        ui_init(disp);
        lvgl_port_unlock();
    }
    board_backlight_set(cfg.backlight);

    net_set_link_cb(on_link);
    net_init();

#if CONFIG_SDS_WEB_SERVER
    web_start();
#endif
    ESP_LOGI(TAG, "startup complete");
}
