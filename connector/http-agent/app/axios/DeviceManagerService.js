const {
  WebUtils: { createTokenGen }, Logger,
} = require('@dojot/microservice-sdk');

const logger = new Logger('http-agent:DeviceManagerService');

class DeviceManagerService {
  /**
   * Consumes api that returns data from a specific device
   *
   * @param {string} deviceManagerUrl Url for api that returns data from a specific device
   */
  constructor(deviceManagerRouteUrl, axios) {
    this.deviceManagerRouteUrl = deviceManagerRouteUrl;
    this.axios = axios();
  }

  /**
   * Requires tenant and device ID
   *
   * @param {string} deviceId the device ID
   *
   * @return data from a specific device
   */
  async getDevice(tenant, deviceId) {
    const tokenGen = createTokenGen();
    const token = await tokenGen.generate({ payload: {}, tenant });
    logger.debug(`requesting device ${deviceId}`);
    const messageKey = await this.axios.get(`${this.deviceManagerRouteUrl}/${deviceId}`, {
      headers: {
        Authorization: `Bearer ${token}`,
      },
    });
    return messageKey.data;
  }
}

module.exports = DeviceManagerService;
