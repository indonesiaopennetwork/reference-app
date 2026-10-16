// Sample products for the Add Product page's Auto-fill, per ION CRC: the
// common fields (EMPTY_RESOURCE's keys), the CRC's attributes in the form's
// shape (numbers as strings, quantities as { unitQuantity, unitText }), and
// seller-defined attributes. Indonesian products, described the way sellers on
// SMESCO and Indonesian marketplaces do.
//
// Brands are real; model numbers, certificate numbers and TKDN figures are
// illustrative sample values, not real registrations. Images are from
// Wikimedia Commons (CC BY / CC BY-SA / public domain).

const COMMONS = 'https://thumb.wikimedia.org/wikipedia/commons/thumb'

export const CRC_AUTOFILL = {
  'TRC-fashion': [
    {
      res: {
        id: 'res-kemeja-batik-tulis-parang',
        name: 'Kemeja Batik Tulis Parang Pria Lengan Panjang (Men’s Hand-drawn Batik Shirt)',
        short_desc: 'Batik tulis Pekalongan motif Parang, katun primissima, furing katun',
        long_desc: 'Kemeja pria dari kain batik tulis asli Pekalongan dengan motif Parang Rusak yang tegas dan berwibawa. Dibuat dengan canting oleh pengrajin Kauman dan diwarnai pewarna alam, sehingga setiap kemeja memiliki guratan yang sedikit berbeda. Kain katun primissima terasa halus dan adem, dilengkapi furing katun agar jatuhnya rapi dan tidak menerawang. Cocok untuk ke kantor, acara resmi, maupun kondangan.',
        media_url: `${COMMONS}/4/4e/Kemeja_batik_Tiga_Negeri.jpg/960px-Kemeja_batik_Tiga_Negeri.jpg`,
        availability: 'IN_STOCK', max_order_qty: '10',
        product_highlights: 'Batik tulis asli, bukan printing\nPewarna alam\nFuring katun, tidak menerawang\nKerah shanghai',
        specifications: 'Bahan: katun primissima\nTeknik: batik tulis\nMotif: Parang Rusak\nUkuran: L',
        brand: 'Batik Pesisir', origin_country: 'ID',
        weight_value: '300', weight_unit: 'G', color: 'Cokelat Soga', material: 'Katun Primissima', finish: 'Matte',
        mfr_type: 'MANUFACTURER', mfr_name: 'Batik Pesisir Kauman', mfr_address: 'Kauman, Pekalongan, Jawa Tengah, ID',
        common_name: 'Kemeja Batik Pria', net_qty_value: '1', net_qty_unit: 'PCS',
      },
      crc: {
        fashion: {
          gender: 'MALE', size: 'L', fabric: 'Katun Primissima', fabricComposition: '100% Katun',
          garmentType: 'SHIRT', textileType: 'BATIK', productionTechnique: 'BATIK_TULIS', motif: 'Parang Rusak',
          dyeType: 'NATURAL', handmade: true, lining: 'Furing katun', fit: 'REGULAR', sleeveLength: 'LONG',
          neckline: 'Kerah shanghai', occasion: ['OFFICE', 'FORMAL', 'WEDDING'],
          measurements: { chestCm: '112', lengthCm: '74', shoulderCm: '46', sleeveLengthCm: '61' },
          careInstructions: ['HAND_WASH', 'DRY_IN_SHADE', 'DO_NOT_BLEACH', 'IRON_LOW'],
        },
      },
      custom: [
        { name: 'Pengrajin', value: 'Kelompok Batik Tulis Kauman, Pekalongan', unit: '' },
        { name: 'Waktu pengerjaan', value: '21', unit: 'hari' },
      ],
    },
    {
      res: {
        id: 'res-blazer-tenun-troso',
        name: 'Blazer Outer Tenun Troso Unisex (Troso Woven Blazer)',
        short_desc: 'Tenun Troso Jepara motif geometris, lapisan trikot halus',
        long_desc: 'Blazer outer dari kain tenun Troso asli Jepara yang memadukan tradisi dan gaya modern. Motif geometris khas Troso memberi karakter kuat, dengan warna netral yang mudah dipadukan untuk suasana formal maupun santai. Dilengkapi lapisan trikot halus sehingga jatuhnya rapi dan structured, nyaman dipakai seharian, dan tidak menerawang.',
        media_url: `${COMMONS}/0/06/Tenun_ikat_troso.jpg/960px-Tenun_ikat_troso.jpg`,
        availability: 'IN_STOCK', max_order_qty: '10',
        product_highlights: 'Tenun ATBM asli Troso\nLapisan trikot, tidak gerah\nModel unisex',
        specifications: 'Bahan: tenun Troso\nLapisan: trikot\nUkuran: M',
        brand: 'Wastra Troso', origin_country: 'ID',
        weight_value: '380', weight_unit: 'G', color: 'Hitam - Abu', material: 'Tenun Troso', finish: 'Matte',
        mfr_type: 'MANUFACTURER', mfr_name: 'Wastra Troso', mfr_address: 'Troso, Pecangaan, Jepara, Jawa Tengah, ID',
        common_name: 'Blazer Tenun', net_qty_value: '1', net_qty_unit: 'PCS',
      },
      crc: {
        fashion: {
          gender: 'UNISEX', size: 'M', fabric: 'Tenun Troso', fabricComposition: '70% Katun, 30% Rayon',
          garmentType: 'OUTERWEAR', textileType: 'TENUN', productionTechnique: 'TENUN_ATBM', motif: 'Geometris Troso',
          dyeType: 'SYNTHETIC', handmade: true, lining: 'Trikot halus', fit: 'REGULAR', sleeveLength: 'LONG',
          neckline: 'Kerah jas', occasion: ['OFFICE', 'CASUAL', 'FORMAL'],
          measurements: { chestCm: '104', lengthCm: '68', shoulderCm: '44', sleeveLengthCm: '58' },
          careInstructions: ['DRY_CLEAN_ONLY', 'DRY_IN_SHADE'],
        },
      },
      custom: [
        { name: 'Asal tenun', value: 'Desa Troso, Jepara', unit: '' },
      ],
    },
    {
      res: {
        id: 'res-kebaya-kutubaru-brokat',
        name: 'Kebaya Kutubaru Brokat Wanita (Women’s Lace Kutubaru Kebaya)',
        short_desc: 'Kebaya kutubaru brokat dengan bef, untuk wisuda dan kondangan',
        long_desc: 'Kebaya kutubaru dari brokat halus dengan detail bef di bagian depan yang memberi kesan anggun dan klasik. Potongan slim mengikuti bentuk tubuh, lengan panjang dengan ujung renda. Cocok dipadukan dengan kain batik atau rok songket untuk wisuda, lamaran, kondangan, dan acara adat.',
        media_url: `${COMMONS}/8/8e/Kebaya_Nusantara.jpg/960px-Kebaya_Nusantara.jpg`,
        availability: 'LOW_STOCK', max_order_qty: '2',
        product_highlights: 'Brokat halus dengan bef\nFuring satin\nCocok untuk acara adat dan wisuda',
        specifications: 'Bahan: brokat\nFuring: satin\nUkuran: S',
        brand: 'Kebaya Ayu', origin_country: 'ID',
        weight_value: '250', weight_unit: 'G', color: 'Merah Marun', material: 'Brokat', finish: 'Satin',
        mfr_type: 'MANUFACTURER', mfr_name: 'Konveksi Kebaya Ayu', mfr_address: 'Laweyan, Surakarta, Jawa Tengah, ID',
        common_name: 'Kebaya Wanita', net_qty_value: '1', net_qty_unit: 'PCS',
      },
      crc: {
        fashion: {
          gender: 'FEMALE', size: 'S', fabric: 'Brokat', fabricComposition: '100% Poliester',
          garmentType: 'KEBAYA', textileType: 'OTHER', productionTechnique: 'MACHINE_EMBROIDERY', motif: 'Bunga mawar',
          dyeType: 'SYNTHETIC', handmade: false, lining: 'Furing satin', fit: 'SLIM', sleeveLength: 'LONG',
          neckline: 'Kutubaru', occasion: ['WEDDING', 'TRADITIONAL_CEREMONY', 'PARTY'],
          measurements: { chestCm: '88', waistCm: '72', lengthCm: '62', sleeveLengthCm: '56' },
          careInstructions: ['HAND_WASH', 'DO_NOT_WRING', 'IRON_LOW'],
        },
      },
      custom: [
        { name: 'Model', value: 'Kutubaru dengan bef', unit: '' },
      ],
    },
    {
      res: {
        id: 'res-kain-songket-palembang',
        name: 'Kain Songket Palembang Motif Lepus (Palembang Songket Cloth)',
        short_desc: 'Songket tenun tangan benang emas, motif Lepus Berakam',
        long_desc: 'Kain songket khas Palembang yang ditenun tangan dengan benang emas di atas kain sutra. Motif Lepus Berakam memenuhi hampir seluruh permukaan kain, tanda songket kelas atas yang dulu dikenakan bangsawan. Satu lembar dikerjakan berminggu-minggu oleh pengrajin. Untuk pernikahan, acara adat, atau koleksi wastra.',
        media_url: `${COMMONS}/d/de/Melestarikan_Songket_Palembang.jpg/960px-Melestarikan_Songket_Palembang.jpg`,
        availability: 'LOW_STOCK', max_order_qty: '1',
        product_highlights: 'Tenun tangan dengan benang emas\nMotif Lepus Berakam\nUkuran 200 x 90 cm',
        specifications: 'Bahan: sutra, benang emas\nUkuran: 200 x 90 cm',
        brand: 'Songket Fikri', origin_country: 'ID',
        weight_value: '600', weight_unit: 'G', color: 'Merah - Emas', material: 'Sutra', finish: 'Glossy',
        mfr_type: 'MANUFACTURER', mfr_name: 'Songket Fikri', mfr_address: 'Tangga Buntung, Palembang, Sumatera Selatan, ID',
        common_name: 'Kain Songket', net_qty_value: '1', net_qty_unit: 'PCS',
      },
      crc: {
        fashion: {
          gender: 'FEMALE', size: 'All size', fabric: 'Sutra dan benang emas', fabricComposition: '80% Sutra, 20% Benang emas',
          garmentType: 'FABRIC', textileType: 'SONGKET', productionTechnique: 'TENUN_ATBM', motif: 'Lepus Berakam',
          dyeType: 'SYNTHETIC', handmade: true, occasion: ['WEDDING', 'TRADITIONAL_CEREMONY'],
          measurements: { lengthCm: '200' },
          careInstructions: ['DRY_CLEAN_ONLY', 'DRY_IN_SHADE'],
        },
      },
      custom: [
        { name: 'Lebar kain', value: '90', unit: 'cm' },
        { name: 'Waktu pengerjaan', value: '30', unit: 'hari' },
      ],
    },
    {
      res: {
        id: 'res-kemeja-karawo-gorontalo',
        name: 'Kemeja Karawo Gorontalo Pria Lengan Pendek (Men’s Karawo Embroidered Shirt)',
        short_desc: 'Sulaman karawo tangan khas Gorontalo motif cengkeh, katun',
        long_desc: 'Kemeja pria dengan sulaman karawo, kerajinan khas Gorontalo yang dibuat dengan mengiris dan mencabut benang kain lalu menyulam motif dengan tangan. Motif bunga cengkeh di bagian dada memberi kesan elegan tanpa berlebihan. Bahan katun yang adem, cocok untuk ke kantor dan acara santai.',
        media_url: `${COMMONS}/3/3a/Sulaman_Karawo.jpg/960px-Sulaman_Karawo.jpg`,
        availability: 'IN_STOCK', max_order_qty: '10',
        product_highlights: 'Sulaman karawo tangan\nKatun adem\nMotif bunga cengkeh',
        specifications: 'Bahan: katun\nTeknik: sulaman karawo\nUkuran: XL',
        brand: 'Karawo Hulonthalo', origin_country: 'ID',
        weight_value: '260', weight_unit: 'G', color: 'Biru Navy', material: 'Katun', finish: 'Matte',
        mfr_type: 'MANUFACTURER', mfr_name: 'Karawo Hulonthalo', mfr_address: 'Kota Gorontalo, Gorontalo, ID',
        common_name: 'Kemeja Karawo', net_qty_value: '1', net_qty_unit: 'PCS',
      },
      crc: {
        fashion: {
          gender: 'MALE', size: 'XL', fabric: 'Katun', fabricComposition: '100% Katun',
          garmentType: 'SHIRT', textileType: 'KARAWO', productionTechnique: 'HAND_EMBROIDERY', motif: 'Bunga cengkeh',
          dyeType: 'SYNTHETIC', handmade: true, fit: 'REGULAR', sleeveLength: 'SHORT',
          neckline: 'Kerah biasa', occasion: ['CASUAL', 'OFFICE'],
          measurements: { chestCm: '120', lengthCm: '76', shoulderCm: '48', sleeveLengthCm: '25' },
          careInstructions: ['HAND_WASH', 'DRY_IN_SHADE', 'IRON_LOW'],
        },
      },
      custom: [
        { name: 'Pengrajin', value: 'Kelompok penyulam karawo Limboto', unit: '' },
      ],
    },
  ],

  'TRC-electronics': [
    {
      res: {
        id: 'res-samsung-galaxy-a55',
        name: 'Samsung Galaxy A55 5G 8/256GB (Garansi Resmi SEIN)',
        short_desc: 'Layar Super AMOLED 6,6", Exynos 1480, baterai 5000 mAh, IP67',
        long_desc: 'Samsung Galaxy A55 5G dengan layar Super AMOLED 6,6 inci 120Hz, prosesor Exynos 1480, RAM 8GB dan memori 256GB. Kamera utama 50MP dengan OIS, baterai 5000 mAh dengan pengisian cepat 25W, serta tahan air dan debu IP67. Unit resmi Indonesia bergaransi Samsung Electronics Indonesia (SEIN) dan terdaftar IMEI.',
        media_url: `${COMMONS}/d/d4/Samsung_Galaxy_S4_mini_Frontside.jpg/960px-Samsung_Galaxy_S4_mini_Frontside.jpg`,
        availability: 'IN_STOCK', max_order_qty: '5',
        product_highlights: 'Garansi resmi SEIN 1 tahun\nIMEI terdaftar\nTahan air IP67\n5G',
        specifications: 'Layar: 6,6" Super AMOLED 120Hz\nProsesor: Exynos 1480\nRAM/ROM: 8/256GB\nBaterai: 5000 mAh',
        brand: 'Samsung', origin_country: 'ID',
        weight_value: '213', weight_unit: 'G', dim_unit: 'CM', dim_length: '16.1', dim_breadth: '7.7', dim_height: '0.8',
        color: 'Awesome Navy', material: 'Kaca dan aluminium', finish: 'Glossy',
        mfr_type: 'MANUFACTURER', mfr_name: 'PT Samsung Electronics Indonesia', mfr_address: 'Cikarang, Bekasi, Jawa Barat, ID',
        common_name: 'Telepon Seluler', net_qty_value: '1', net_qty_unit: 'PCS',
      },
      crc: {
        electronics: {
          deviceType: 'SMARTPHONE', modelNumber: 'SM-A556E', osType: 'ANDROID',
          connectivity: ['WIFI', 'BLUETOOTH', 'NFC', 'USB_C', 'SUB6_5G', '4G_LTE'],
          processor: 'Exynos 1480', ram: { unitQuantity: '8', unitText: 'GB' }, storage: { unitQuantity: '256', unitText: 'GB' },
          screenSizeInch: '6.6', displayType: 'Super AMOLED 120Hz', batteryCapacity: { unitQuantity: '5000', unitText: 'mAh' },
          wattage: { unitQuantity: '25', unitText: 'W' }, powerSource: 'RECHARGEABLE_BATTERY', plugType: 'NONE',
          ipRating: 'IP67', warrantyType: 'OFFICIAL', sdppiCertificate: '12345/SDPPI/2024', tkdnPercentage: '35',
          inTheBox: 'Kabel USB-C, Ejector pin, Buku panduan',
        },
      },
      custom: [
        { name: 'Kamera utama', value: '50', unit: 'MP' },
        { name: 'IMEI terdaftar', value: 'Ya', unit: '' },
      ],
    },
    {
      res: {
        id: 'res-polytron-smart-tv-43',
        name: 'Polytron Smart Digital TV 43 Inch Full HD (Garansi Resmi)',
        short_desc: 'Google TV, Full HD, siaran digital DVB-T2, garansi panel 3 tahun',
        long_desc: 'Televisi pintar Polytron 43 inci buatan Kudus dengan resolusi Full HD dan sistem Google TV untuk YouTube, Netflix dan aplikasi lainnya. Sudah mendukung siaran TV digital DVB-T2 tanpa set top box. Suara jernih dengan speaker 2 x 10W, hemat energi, dan didukung layanan purna jual Polytron di seluruh Indonesia.',
        media_url: 'https://upload.wikimedia.org/wikipedia/commons/b/b4/LED_TV.jpg',
        availability: 'IN_STOCK', max_order_qty: '5',
        product_highlights: 'Google TV\nTV digital DVB-T2 tanpa STB\nGaransi panel 3 tahun\nProduk dalam negeri',
        specifications: 'Ukuran: 43 inci\nResolusi: Full HD 1920 x 1080\nSpeaker: 2 x 10W',
        brand: 'Polytron', origin_country: 'ID',
        weight_value: '6.5', weight_unit: 'KG', dim_unit: 'CM', dim_length: '96.5', dim_breadth: '8.5', dim_height: '56.8',
        color: 'Hitam', material: 'Plastik', finish: 'Matte',
        mfr_type: 'MANUFACTURER', mfr_name: 'PT Hartono Istana Teknologi', mfr_address: 'Kudus, Jawa Tengah, ID',
        common_name: 'Televisi LED', net_qty_value: '1', net_qty_unit: 'PCS',
      },
      crc: {
        electronics: {
          deviceType: 'TELEVISION', modelNumber: 'PLD 43BUG5959', osType: 'ANDROID',
          connectivity: ['WIFI', 'BLUETOOTH', 'HDMI', 'USB_A', 'ETHERNET'],
          screenSizeInch: '43', displayType: 'LED Full HD',
          wattage: { unitQuantity: '75', unitText: 'W' }, voltage: { unitQuantity: '220', unitText: 'V' },
          energyRating: '4', powerSource: 'MAINS_220V', plugType: 'TYPE_C',
          warrantyType: 'OFFICIAL', sdppiCertificate: '23456/SDPPI/2025', tkdnPercentage: '40',
          inTheBox: 'Remote, Kaki TV, Kabel power, Buku manual',
        },
      },
      custom: [
        { name: 'Garansi panel', value: '3', unit: 'tahun' },
        { name: 'Tuner digital', value: 'DVB-T2', unit: '' },
      ],
    },
    {
      res: {
        id: 'res-advan-workpro-laptop',
        name: 'Advan WorkPro Laptop 14" Ryzen 5 16/512GB Windows 11',
        short_desc: 'Laptop kerja produk Indonesia, layar IPS 14", RAM 16GB, SSD 512GB',
        long_desc: 'Laptop Advan WorkPro dengan prosesor AMD Ryzen 5, RAM 16GB dan SSD 512GB untuk kerja kantor, belajar dan multitasking. Layar 14 inci IPS Full HD, bodi ringan 1,4 kg, baterai tahan hingga 8 jam, dan Windows 11 Home original. Produk dalam negeri dengan garansi resmi Advan dan service center di berbagai kota.',
        media_url: `${COMMONS}/3/37/Schenker_VIA14_Laptop_asv2021-01.jpg/960px-Schenker_VIA14_Laptop_asv2021-01.jpg`,
        availability: 'IN_STOCK', max_order_qty: '5',
        product_highlights: 'Windows 11 original\nSSD 512GB\nProduk dalam negeri\nGaransi resmi 1 tahun',
        specifications: 'Prosesor: AMD Ryzen 5 6600H\nRAM: 16GB\nSSD: 512GB\nLayar: 14" IPS FHD',
        brand: 'Advan', origin_country: 'ID',
        weight_value: '1.4', weight_unit: 'KG', dim_unit: 'CM', dim_length: '32.2', dim_breadth: '21.3', dim_height: '1.8',
        color: 'Silver', material: 'Aluminium', finish: 'Matte',
        mfr_type: 'MANUFACTURER', mfr_name: 'PT Bangga Teknologi Indonesia', mfr_address: 'Semarang, Jawa Tengah, ID',
        common_name: 'Komputer Jinjing', net_qty_value: '1', net_qty_unit: 'PCS',
      },
      crc: {
        electronics: {
          deviceType: 'LAPTOP', modelNumber: 'WorkPro-R5-14', osType: 'WINDOWS',
          connectivity: ['WIFI', 'BLUETOOTH', 'USB_C', 'USB_A', 'HDMI'],
          processor: 'AMD Ryzen 5 6600H', ram: { unitQuantity: '16', unitText: 'GB' }, storage: { unitQuantity: '512', unitText: 'GB' },
          screenSizeInch: '14', displayType: 'IPS Full HD', batteryCapacity: { unitQuantity: '50', unitText: 'Wh' },
          wattage: { unitQuantity: '65', unitText: 'W' }, powerSource: 'RECHARGEABLE_BATTERY', plugType: 'TYPE_C',
          warrantyType: 'OFFICIAL', sdppiCertificate: '34567/SDPPI/2025', tkdnPercentage: '40',
          inTheBox: 'Adaptor 65W, Kartu garansi, Buku panduan',
        },
      },
      custom: [
        { name: 'Sistem operasi', value: 'Windows 11 Home original', unit: '' },
      ],
    },
    {
      res: {
        id: 'res-miyako-magic-com-18',
        name: 'Miyako Magic Com Rice Cooker 1,8 Liter 3in1',
        short_desc: 'Penanak nasi 3in1: memasak, menghangatkan, mengukus; SNI',
        long_desc: 'Rice cooker Miyako 3in1 kapasitas 1,8 liter untuk memasak nasi, menghangatkan dan mengukus. Panci anti lengket mudah dibersihkan, dilengkapi rak kukusan, centong dan gelas takar. Daya 350W hemat listrik, sudah ber-SNI, dan bergaransi resmi Miyako.',
        media_url: 'https://upload.wikimedia.org/wikipedia/commons/9/96/Rice-cooker.jpg',
        availability: 'IN_STOCK', max_order_qty: '10',
        product_highlights: 'Kapasitas 1,8 liter\nMemasak, menghangatkan, mengukus\nPanci anti lengket\nBer-SNI',
        specifications: 'Kapasitas: 1,8 L\nDaya: 350 W\nTegangan: 220 V',
        brand: 'Miyako', origin_country: 'ID',
        weight_value: '2.3', weight_unit: 'KG', dim_unit: 'CM', dim_length: '28', dim_breadth: '27', dim_height: '27',
        color: 'Putih motif bunga', material: 'Plastik dan aluminium', finish: 'Glossy',
        mfr_type: 'MANUFACTURER', mfr_name: 'PT Kencana Gemilang', mfr_address: 'Tangerang, Banten, ID',
        common_name: 'Penanak Nasi Listrik', net_qty_value: '1', net_qty_unit: 'PCS',
      },
      crc: {
        electronics: {
          deviceType: 'KITCHEN_APPLIANCE', modelNumber: 'MCM-528',
          wattage: { unitQuantity: '350', unitText: 'W' }, voltage: { unitQuantity: '220', unitText: 'V' },
          powerSource: 'MAINS_220V', plugType: 'TYPE_C', warrantyType: 'OFFICIAL',
          inTheBox: 'Panci, Rak kukusan, Centong, Gelas takar, Kabel power',
        },
      },
      custom: [
        { name: 'Kapasitas', value: '1.8', unit: 'liter' },
        { name: 'Fungsi', value: 'Memasak, menghangatkan, mengukus', unit: '' },
      ],
    },
    {
      res: {
        id: 'res-robot-airbuds-tws',
        name: 'Robot Airbuds T10 TWS Earphone Bluetooth 5.3',
        short_desc: 'TWS Bluetooth 5.3, tahan cipratan IPX4, baterai hingga 30 jam',
        long_desc: 'Earphone true wireless Robot Airbuds T10 dengan Bluetooth 5.3 untuk koneksi stabil dan latensi rendah. Suara bass bertenaga, kontrol sentuh, dan tahan cipratan air IPX4. Baterai earbud 6 jam dan total hingga 30 jam dengan charging case USB-C. Garansi resmi Robot Indonesia.',
        media_url: `${COMMONS}/9/90/ActiveSound_wireless_earbuds_by_Hykker_%28POJM200483%29.jpg/960px-ActiveSound_wireless_earbuds_by_Hykker_%28POJM200483%29.jpg`,
        availability: 'IN_STOCK', max_order_qty: '10',
        product_highlights: 'Bluetooth 5.3\nTahan cipratan IPX4\nTotal 30 jam dengan case\nGaransi resmi',
        specifications: 'Bluetooth: 5.3\nBaterai case: 300 mAh\nPengisian: USB-C',
        brand: 'Robot', origin_country: 'ID',
        weight_value: '45', weight_unit: 'G', dim_unit: 'CM', dim_length: '6', dim_breadth: '4.8', dim_height: '2.5',
        color: 'Hitam', material: 'Plastik ABS', finish: 'Matte',
        mfr_type: 'IMPORTER', mfr_name: 'PT Robot Indonesia', mfr_address: 'Jakarta Utara, DKI Jakarta, ID',
        common_name: 'Earphone Nirkabel', net_qty_value: '1', net_qty_unit: 'PCS',
      },
      crc: {
        electronics: {
          deviceType: 'AUDIO', modelNumber: 'Airbuds T10', connectivity: ['BLUETOOTH', 'USB_C'],
          batteryCapacity: { unitQuantity: '300', unitText: 'mAh' }, powerSource: 'RECHARGEABLE_BATTERY', plugType: 'NONE',
          ipRating: 'IPX4', warrantyType: 'OFFICIAL', sdppiCertificate: '45678/SDPPI/2025',
          inTheBox: 'Earbuds, Charging case, Kabel USB-C, Ear tips cadangan',
        },
      },
      custom: [
        { name: 'Versi Bluetooth', value: '5.3', unit: '' },
        { name: 'Daya tahan baterai total', value: '30', unit: 'jam' },
      ],
    },
  ],
}
