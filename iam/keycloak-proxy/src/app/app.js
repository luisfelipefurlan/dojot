const ExpressAdapter = require('./web/express-adapter');
const routesV1 = require('./web/routesV1');
const Dependencies = require('./dependencies');

module.exports = class App {
  /**
   * App
   *
   * @param {Server} server Dojot server
   * @param {Logger} logger Dojot logger
   * @param {string} logger The path to the yml file
   */
  constructor(config, logger, openApiPath) {
    this.logger = logger;
    this.openApiPath = openApiPath;
    this.config = config;
  }

  /**
   * Initializes the application
   *
   */
  async init() {
    // Initialize internal dependencies
    const web = Dependencies(this.config, this.logger);
    this.server = web.httpServer;
    this.keycloakSession = web.keycloakAdminSession;
    await web.keycloakAdminSession.start();
    await web.keycloakApiAdapter.init();

    try {
      // Adapts express to the application and manages the routes
      this.express = ExpressAdapter.adapt(
        routesV1('/api/v1', web.controllers),
        this.server.serviceState,
        this.openApiPath,
        this.logger,
        this.config,
      );
      this.server.init(this.express);
      this.server.on('close', this.finish);
    } catch (error) {
      this.logger.error(error);
      this.finish();
      throw error;
    }
  }

  /**
   * Finished application
   */
  finish() {
    this.keycloakSession.close();
  }
};
