include $(TOPDIR)/rules.mk

PKG_NAME:=fssrl-client
PKG_RELEASE:=1

FSSRL_SRC_DIR:=../../client
FSSRL_CONTRIB_DIR:=../../contrib

include $(INCLUDE_DIR)/package.mk

define Package/fssrl-client
  SECTION:=net
  CATEGORY:=Network
  TITLE:=fastd server-side ratelimit client
  DEPENDS:=+tc-tiny +kmod-sched-cake
endef

define Package/fssrl-client/description
 Client daemon for fastd server-side rate limiting.
endef

define Package/fssrl-client/conffiles
/etc/config/fssrl
endef

define Build/Prepare
	$(INSTALL_DIR) $(PKG_BUILD_DIR)
	$(CP) $(FSSRL_SRC_DIR)/src/* $(PKG_BUILD_DIR)/
endef

define Package/fssrl-client/install
	$(INSTALL_DIR) $(1)/usr/sbin
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/fssrl-client $(1)/usr/sbin/fssrl-client

	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) $(FSSRL_CONTRIB_DIR)/client/fssrl.init $(1)/etc/init.d/fssrl

	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_DATA) $(FSSRL_CONTRIB_DIR)/client/config.uci $(1)/etc/config/fssrl

	$(INSTALL_DIR) $(1)/usr/share/fssrl
	$(INSTALL_BIN) $(FSSRL_CONTRIB_DIR)/apply-shaper.sh $(1)/usr/share/fssrl/apply-rate-limit.sh
endef

define Package/fssrl-client-gluon
  SECTION:=net
  CATEGORY:=Network
  TITLE:=fastd server-side ratelimit client (gluon)
  DEPENDS:=+fssrl-client
endef

define Package/fssrl-client-gluon/install
	$(INSTALL_DIR) $(1)/lib/gluon/upgrade
	$(INSTALL_BIN) $(FSSRL_CONTRIB_DIR)/client/gluon-upgrade.lua $(1)/lib/gluon/upgrade/990-fssrl-client
endef

$(eval $(call BuildPackage,fssrl-client))
$(eval $(call BuildPackage,fssrl-client-gluon))