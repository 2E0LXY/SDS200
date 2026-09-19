idf_component_register(
    SRCS "main.c" "settings.c" "board.c" "panel_init.c" "net.c" "scanner.c" "audio.c" "ui.c" "web.c"
    INCLUDE_DIRS "."
    REQUIRES esp_lcd esp_driver_gpio esp_driver_ledc esp_driver_i2c esp_driver_i2s esp_timer esp_wifi
             esp_netif esp_event nvs_flash esp_http_server json lwip esp_app_format esp_psram)
