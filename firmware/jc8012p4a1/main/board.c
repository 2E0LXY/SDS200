/*
 * Board support for the Guition JC8012P4A1.
 */
#include <string.h>
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"
#include "esp_log.h"
#include "esp_check.h"
#include "driver/gpio.h"
#include "driver/ledc.h"
#include "driver/i2c_master.h"
#include "driver/i2s_std.h"
#include "esp_ldo_regulator.h"
#include "esp_lcd_panel_ops.h"
#include "esp_lcd_panel_io.h"
#include "esp_lcd_mipi_dsi.h"
#include "esp_lcd_jd9365.h"
#include "esp_lcd_touch.h"
#include "esp_lcd_gsl3680.h"   /* NB: defines the touch firmware table; include once only */
#include "esp_lvgl_port.h"
#include "esp_codec_dev.h"
#include "esp_codec_dev_defaults.h"
#include "sdkconfig.h"
#include "app_config.h"
#include "panel_init.h"
#include "board.h"

static const char *TAG = "board";

static i2c_master_bus_handle_t s_i2c;
static lv_display_t *s_disp;
static esp_lcd_touch_handle_t s_touch;

/* ------------------------------------------------------------------ I2C -- */

esp_err_t board_i2c_init(void)
{
    const i2c_master_bus_config_t cfg = {
        .i2c_port = I2C_NUM_0,
        .sda_io_num = PIN_I2C_SDA,
        .scl_io_num = PIN_I2C_SCL,
        .clk_source = I2C_CLK_SRC_DEFAULT,
        .glitch_ignore_cnt = 7,
        .flags.enable_internal_pullup = false,   /* board has external pull-ups */
    };
    return i2c_new_master_bus(&cfg, &s_i2c);
}

/* ------------------------------------------------------------ backlight -- */

#define BL_LEDC_TIMER   LEDC_TIMER_0
#define BL_LEDC_CH      LEDC_CHANNEL_0
#define BL_LEDC_RES     LEDC_TIMER_10_BIT

esp_err_t board_backlight_init(void)
{
    const ledc_timer_config_t t = {
        .speed_mode = LEDC_LOW_SPEED_MODE,
        .duty_resolution = BL_LEDC_RES,
        .timer_num = BL_LEDC_TIMER,
        .freq_hz = 20000,
        .clk_cfg = LEDC_AUTO_CLK,
    };
    ESP_RETURN_ON_ERROR(ledc_timer_config(&t), TAG, "ledc timer");
    const ledc_channel_config_t c = {
        .gpio_num = PIN_LCD_BL,
        .speed_mode = LEDC_LOW_SPEED_MODE,
        .channel = BL_LEDC_CH,
        .timer_sel = BL_LEDC_TIMER,
        .duty = 0,               /* dark until the panel is initialised */
        .hpoint = 0,
    };
    return ledc_channel_config(&c);
}

void board_backlight_set(uint8_t percent)
{
    if (percent > 100) {
        percent = 100;
    }
    uint32_t duty = ((1u << 10) - 1) * percent / 100;   /* active-high */
    ledc_set_duty(LEDC_LOW_SPEED_MODE, BL_LEDC_CH, duty);
    ledc_update_duty(LEDC_LOW_SPEED_MODE, BL_LEDC_CH);
}

/* -------------------------------------------------------------- display -- */

static esp_err_t touch_init(void)
{
    esp_lcd_panel_io_handle_t io = NULL;
    esp_lcd_panel_io_i2c_config_t io_cfg = ESP_LCD_TOUCH_IO_I2C_GSL3680_CONFIG();
    io_cfg.scl_speed_hz = I2C_FREQ_HZ;
    ESP_RETURN_ON_ERROR(esp_lcd_new_panel_io_i2c(s_i2c, &io_cfg, &io), TAG, "touch io");

    /* Configured in the panel's native portrait frame; LVGL applies the
     * display rotation to pointer coordinates itself. */
    const esp_lcd_touch_config_t tp_cfg = {
        .x_max = LCD_H_RES,
        .y_max = LCD_V_RES,
        .rst_gpio_num = PIN_TOUCH_RST,
        .int_gpio_num = PIN_TOUCH_INT,
        .levels = {
            .reset = 0,
            .interrupt = 0,
        },
        .flags = {
#if CONFIG_SDS_TOUCH_SWAP_XY
            .swap_xy = 1,
#endif
#if CONFIG_SDS_TOUCH_MIRROR_X
            .mirror_x = 1,
#endif
#if CONFIG_SDS_TOUCH_MIRROR_Y
            .mirror_y = 1,
#endif
        },
    };
    return esp_lcd_touch_new_i2c_gsl3680(io, &tp_cfg, &s_touch);
}

lv_display_t *board_display_init(uint8_t panel_rev, bool flip)
{
    /* MIPI D-PHY supply */
    static esp_ldo_channel_handle_t ldo = NULL;
    const esp_ldo_channel_config_t ldo_cfg = {
        .chan_id = LCD_DSI_PHY_LDO_CHAN,
        .voltage_mv = LCD_DSI_PHY_LDO_MV,
    };
    ESP_ERROR_CHECK(esp_ldo_acquire_channel(&ldo_cfg, &ldo));

    /* Timings: original panel per the Guition datasheet / ESPHome model
     * GUITION_JC8012P4A1; V2 per ESPHome model GUITION_JC8012P4A1_V2. */
    const bool v2 = (panel_rev == 2);
    const uint32_t lane_mbps = v2 ? 1500 : 1000;
    const uint32_t pclk_mhz = v2 ? 70 : 60;

    esp_lcd_dsi_bus_handle_t bus = NULL;
    const esp_lcd_dsi_bus_config_t bus_cfg = {
        .bus_id = 0,
        .num_data_lanes = LCD_DSI_LANES,
        .phy_clk_src = MIPI_DSI_PHY_CLK_SRC_DEFAULT,
        .lane_bit_rate_mbps = lane_mbps,
    };
    ESP_ERROR_CHECK(esp_lcd_new_dsi_bus(&bus_cfg, &bus));

    esp_lcd_panel_io_handle_t io = NULL;
    const esp_lcd_dbi_io_config_t dbi_cfg = {
        .virtual_channel = 0,
        .lcd_cmd_bits = 8,
        .lcd_param_bits = 8,
    };
    ESP_ERROR_CHECK(esp_lcd_new_panel_io_dbi(bus, &dbi_cfg, &io));

    const esp_lcd_dpi_panel_config_t dpi_cfg = {
        .virtual_channel = 0,
        .dpi_clk_src = MIPI_DSI_DPI_CLK_SRC_DEFAULT,
        .dpi_clock_freq_mhz = pclk_mhz,
        .pixel_format = LCD_COLOR_PIXEL_FORMAT_RGB565,
        .num_fbs = 1,
        .video_timing = {
            .h_size = LCD_H_RES,
            .v_size = LCD_V_RES,
            .hsync_back_porch = 20,
            .hsync_pulse_width = 20,
            .hsync_front_porch = 40,
            .vsync_back_porch = v2 ? 10 : 8,
            .vsync_pulse_width = 4,
            .vsync_front_porch = 20,
        },
        .flags.use_dma2d = true,
    };
    jd9365_vendor_config_t vendor = {
        .init_cmds = v2 ? jc8012_init_v2 : jc8012_init_v1,
        .init_cmds_size = (uint16_t)(v2 ? jc8012_init_v2_count : jc8012_init_v1_count),
        .mipi_config = {
            .dsi_bus = bus,
            .dpi_config = &dpi_cfg,
            .lane_num = LCD_DSI_LANES,
        },
    };
    const esp_lcd_panel_dev_config_t dev_cfg = {
        .reset_gpio_num = PIN_LCD_RST,
        .rgb_ele_order = LCD_RGB_ELEMENT_ORDER_RGB,
        .bits_per_pixel = 16,
        .vendor_config = &vendor,
    };
    esp_lcd_panel_handle_t panel = NULL;
    ESP_ERROR_CHECK(esp_lcd_new_panel_jd9365(io, &dev_cfg, &panel));
    ESP_ERROR_CHECK(esp_lcd_panel_reset(panel));
    ESP_ERROR_CHECK(esp_lcd_panel_init(panel));
    ESP_LOGI(TAG, "JD9365 panel V%u up: %lu Mbps/lane, %lu MHz pclk", panel_rev,
             (unsigned long)lane_mbps, (unsigned long)pclk_mhz);

    /* LVGL port */
    lvgl_port_cfg_t port_cfg = ESP_LVGL_PORT_INIT_CONFIG();
    port_cfg.task_priority = 4;
    port_cfg.task_stack = 12288;
    port_cfg.task_affinity = 1;
    port_cfg.timer_period_ms = 5;
    ESP_ERROR_CHECK(lvgl_port_init(&port_cfg));

    const lvgl_port_display_cfg_t disp_cfg = {
        .io_handle = io,
        .panel_handle = panel,
        .control_handle = NULL,
        .buffer_size = LCD_H_RES * 128,
        .double_buffer = true,
        .hres = LCD_H_RES,
        .vres = LCD_V_RES,
        .monochrome = false,
        .color_format = LV_COLOR_FORMAT_RGB565,
        .rotation = {
            .swap_xy = false,
            .mirror_x = false,
            .mirror_y = false,
        },
        .flags = {
            .buff_dma = true,
            .buff_spiram = true,
            .sw_rotate = true,      /* with CONFIG_LVGL_PORT_ENABLE_PPA this uses the PPA */
            .swap_bytes = false,
        },
    };
    const lvgl_port_display_dsi_cfg_t dsi_cfg = {
        .flags.avoid_tearing = false,
    };
    s_disp = lvgl_port_add_disp_dsi(&disp_cfg, &dsi_cfg);
    assert(s_disp);

    if (touch_init() == ESP_OK && s_touch) {
        const lvgl_port_touch_cfg_t tcfg = {
            .disp = s_disp,
            .handle = s_touch,
        };
        lvgl_port_add_touch(&tcfg);
    } else {
        ESP_LOGE(TAG, "GSL3680 touch initialisation failed");
    }

    board_display_set_flip(flip);
    return s_disp;
}

void board_display_set_flip(bool flip)
{
    if (!s_disp) {
        return;
    }
#if CONFIG_SDS_ROTATION_270
    lv_display_rotation_t rot = flip ? LV_DISPLAY_ROTATION_90 : LV_DISPLAY_ROTATION_270;
#else
    lv_display_rotation_t rot = flip ? LV_DISPLAY_ROTATION_270 : LV_DISPLAY_ROTATION_90;
#endif
    if (lvgl_port_lock(0)) {
        lv_display_set_rotation(s_disp, rot);
        lvgl_port_unlock();
    }
}

/* ---------------------------------------------------------------- audio -- */

#define AUDIO_RATE_HZ   16000

static i2s_chan_handle_t s_i2s_tx;
static esp_codec_dev_handle_t s_codec;
static bool s_audio_open;
static uint8_t s_audio_vol = 70;

esp_err_t board_audio_init(void)
{
    /* Amplifier off while the codec is configured */
    const gpio_config_t pa = {
        .pin_bit_mask = 1ULL << PIN_PA_EN,
        .mode = GPIO_MODE_OUTPUT,
    };
    gpio_config(&pa);
    gpio_set_level(PIN_PA_EN, 0);

    i2s_chan_config_t chan_cfg = I2S_CHANNEL_DEFAULT_CONFIG(I2S_NUM_0, I2S_ROLE_MASTER);
    chan_cfg.auto_clear = true;
    chan_cfg.dma_desc_num = 6;
    chan_cfg.dma_frame_num = 240;
    ESP_RETURN_ON_ERROR(i2s_new_channel(&chan_cfg, &s_i2s_tx, NULL), TAG, "i2s channel");

    i2s_std_config_t std_cfg = {
        .clk_cfg = I2S_STD_CLK_DEFAULT_CONFIG(AUDIO_RATE_HZ),
        .slot_cfg = I2S_STD_PHILIPS_SLOT_DEFAULT_CONFIG(I2S_DATA_BIT_WIDTH_16BIT, I2S_SLOT_MODE_STEREO),
        .gpio_cfg = {
            .mclk = PIN_I2S_MCLK,
            .bclk = PIN_I2S_BCLK,
            .ws = PIN_I2S_LRCK,
            .dout = PIN_I2S_DOUT,
            .din = I2S_GPIO_UNUSED,
        },
    };
    std_cfg.clk_cfg.mclk_multiple = I2S_MCLK_MULTIPLE_256;
    ESP_RETURN_ON_ERROR(i2s_channel_init_std_mode(s_i2s_tx, &std_cfg), TAG, "i2s std");

    audio_codec_i2s_cfg_t i2s_if_cfg = {
        .port = I2S_NUM_0,
        .rx_handle = NULL,
        .tx_handle = s_i2s_tx,
    };
    const audio_codec_data_if_t *data_if = audio_codec_new_i2s_data(&i2s_if_cfg);
    audio_codec_i2c_cfg_t i2c_if_cfg = {
        .port = I2C_NUM_0,
        .addr = ES8311_CODEC_DEFAULT_ADDR,   /* 8-bit form of 0x18 */
        .bus_handle = s_i2c,
    };
    const audio_codec_ctrl_if_t *ctrl_if = audio_codec_new_i2c_ctrl(&i2c_if_cfg);
    const audio_codec_gpio_if_t *gpio_if = audio_codec_new_gpio();
    ESP_RETURN_ON_FALSE(data_if && ctrl_if && gpio_if, ESP_FAIL, TAG, "codec interfaces");

    es8311_codec_cfg_t es_cfg = {
        .ctrl_if = ctrl_if,
        .gpio_if = gpio_if,
        .codec_mode = ESP_CODEC_DEV_WORK_MODE_DAC,
        .pa_pin = -1,            /* NS4150B enable is driven directly */
        .pa_reverted = false,
        .master_mode = false,
        .use_mclk = true,
        .hw_gain = {
            .pa_voltage = 5.0,
            .codec_dac_voltage = 3.3,
        },
    };
    const audio_codec_if_t *codec_if = es8311_codec_new(&es_cfg);
    ESP_RETURN_ON_FALSE(codec_if, ESP_FAIL, TAG, "es8311_codec_new");

    esp_codec_dev_cfg_t dev_cfg = {
        .dev_type = ESP_CODEC_DEV_TYPE_OUT,
        .codec_if = codec_if,
        .data_if = data_if,
    };
    s_codec = esp_codec_dev_new(&dev_cfg);
    ESP_RETURN_ON_FALSE(s_codec, ESP_FAIL, TAG, "esp_codec_dev_new");
    ESP_LOGI(TAG, "ES8311 codec ready");
    return ESP_OK;
}

esp_err_t board_audio_start(void)
{
    if (!s_codec) {
        return ESP_ERR_INVALID_STATE;
    }
    if (s_audio_open) {
        return ESP_OK;
    }
    esp_codec_dev_sample_info_t fs = {
        .bits_per_sample = 16,
        .channel = 2,
        .channel_mask = 0,
        .sample_rate = AUDIO_RATE_HZ,
        .mclk_multiple = 256,
    };
    if (esp_codec_dev_open(s_codec, &fs) != ESP_CODEC_DEV_OK) {
        return ESP_FAIL;
    }
    esp_codec_dev_set_out_vol(s_codec, s_audio_vol);
    s_audio_open = true;
    gpio_set_level(PIN_PA_EN, 1);
    return ESP_OK;
}

void board_audio_stop(void)
{
    if (!s_audio_open) {
        return;
    }
    gpio_set_level(PIN_PA_EN, 0);
    esp_codec_dev_close(s_codec);
    s_audio_open = false;
}

esp_err_t board_audio_write(const int16_t *frames, size_t n_frames)
{
    if (!s_audio_open) {
        return ESP_ERR_INVALID_STATE;
    }
    int r = esp_codec_dev_write(s_codec, (void *)frames, (int)(n_frames * 2 * sizeof(int16_t)));
    return r == ESP_CODEC_DEV_OK ? ESP_OK : ESP_FAIL;
}

void board_audio_set_volume(uint8_t percent)
{
    s_audio_vol = percent > 100 ? 100 : percent;
    if (s_codec && s_audio_open) {
        esp_codec_dev_set_out_vol(s_codec, s_audio_vol);
    }
}
