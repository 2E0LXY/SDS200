/*
 * Board pin map and application constants for the Guition JC8012P4A1.
 */
#pragma once

#define APP_NAME                "SDS200 Panel"

/* Native panel geometry (portrait); the UI runs in landscape. */
#define LCD_H_RES               800
#define LCD_V_RES               1280
#define UI_H_RES                1280
#define UI_V_RES                800

#define PIN_LCD_RST             27
#define PIN_LCD_BL              23
#define LCD_DSI_LANES           2
#define LCD_DSI_PHY_LDO_CHAN    3
#define LCD_DSI_PHY_LDO_MV      2500

/* Shared I2C bus: GSL3680 touch (0x40) and ES8311 codec (0x18) */
#define PIN_I2C_SDA             7
#define PIN_I2C_SCL             8
#define I2C_FREQ_HZ             400000
#define PIN_TOUCH_INT           21
#define PIN_TOUCH_RST           22

/* ES8311 + NS4150B */
#define PIN_I2S_MCLK            13
#define PIN_I2S_BCLK            12
#define PIN_I2S_LRCK            10
#define PIN_I2S_DOUT            9   /* P4 -> codec DSDIN */
#define PIN_I2S_DIN             11  /* codec ASDOUT -> P4 */
#define PIN_PA_EN               20
#define ES8311_I2C_ADDR_7BIT    0x18

/* Scanner protocol */
#define SCANNER_UDP_PORT        50536
#define SCANNER_RTSP_PORT       554
#define SCANNER_TIMEOUT_MS      1200
